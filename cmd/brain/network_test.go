package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// networkSourceFindings checks authored code. Tests can contain network probes.
func networkSourceFindings(root string) ([]string, error) {
	var findings []string
	for _, folder := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, folder), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			positions := token.NewFileSet()
			file, err := parser.ParseFile(positions, path, nil, 0)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			report := func(position token.Pos, reason string) {
				findings = append(findings, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(relative), positions.Position(position).Line, reason))
			}
			lowLevel := map[string]bool{}
			for _, imported := range file.Imports {
				name, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					return err
				}
				if name == "net" || strings.HasPrefix(name, "net/") || name == "crypto/tls" {
					report(imported.Pos(), "network import "+name)
				} else if name != "modernc.org/sqlite" && !strings.HasPrefix(name, "brain/") {
					info, problem := os.Stat(filepath.Join(runtime.GOROOT(), "src", filepath.FromSlash(name)))
					if problem != nil || !info.IsDir() {
						report(imported.Pos(), "unapproved dependency "+name)
					}
				}
				if name == "syscall" || name == "golang.org/x/sys/unix" || name == "golang.org/x/sys/windows" {
					alias := filepath.Base(name)
					if imported.Name != nil {
						alias = imported.Name.Name
					}
					lowLevel[alias] = true
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if alias, ok := selector.X.(*ast.Ident); ok && lowLevel[alias.Name] && socketAPI(selector.Sel.Name) {
						report(selector.Pos(), "socket API "+alias.Name+"."+selector.Sel.Name)
					}
				}
				if identifier, ok := node.(*ast.Ident); ok && lowLevel["."] && socketAPI(identifier.Name) {
					report(identifier.Pos(), "socket API "+identifier.Name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func socketAPI(name string) bool {
	switch name {
	case "Socket", "Socketpair", "Connect", "Bind", "Listen", "Accept", "Accept4", "Sendto", "Recvfrom", "Sendmsg", "Recvmsg",
		"WSASocket", "WSASocketW", "WSAConnect", "WSASend", "WSASendTo", "WSARecv", "WSARecvFrom",
		"SYS_SOCKET", "SYS_SOCKETPAIR", "SYS_SOCKETCALL", "SYS_CONNECT", "SYS_BIND", "SYS_LISTEN", "SYS_ACCEPT", "SYS_ACCEPT4", "SYS_SENDTO", "SYS_RECVFROM", "SYS_SENDMSG", "SYS_RECVMSG":
		return true
	}
	return false
}

func TestNoNetworkImports(t *testing.T) {
	findings, err := networkSourceFindings(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatal(strings.Join(findings, "\n"))
	}
}

func TestNetworkSourceGuard(t *testing.T) {
	for _, test := range []struct {
		name, source string
		want         bool
	}{
		{"net", `package fixture; import "net"`, true},
		{"net-subpackage", `package fixture; import _ "net/url"`, true},
		{"tls", `package fixture; import secure "crypto/tls"`, true},
		{"socket-alias", `package fixture; import s "syscall"; var open = s.Socket`, true},
		{"socket-dot", `package fixture; import . "syscall"; func f() { Socket(0, 0, 0) }`, true},
		{"socket-unix", `package fixture; import u "golang.org/x/sys/unix"; func f() { u.Connect(0, nil) }`, true},
		{"socket-windows", `package fixture; import w "golang.org/x/sys/windows"; var open = w.WSASocket`, true},
		{"raw-socket", `package fixture; import "syscall"; var number = syscall.SYS_SOCKET`, true},
		{"filesystem", `package fixture; import "syscall"; var lock = syscall.Flock`, false},
		{"sqlite", `package fixture; import _ "modernc.org/sqlite"`, false},
		{"transitive-module", `package fixture; import _ "golang.org/x/sys/windows"`, true},
		{"other-module", `package fixture; import _ "example.org/extra"`, true},
		{"comment", "package fixture\n// import \"net\"\nvar text = \"net/http\"", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, folder := range []string{"cmd", "internal", "internal/testdata"} {
				if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{"cmd/fixture.go", "internal/fixture.go"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(test.source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range []string{"cmd/probe_test.go", "internal/testdata/probe.go"} {
				if err := os.WriteFile(filepath.Join(root, file), []byte(`package fixture; import "net"`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			findings, err := networkSourceFindings(root)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if test.want {
				want = 2
			}
			if (want == 0 && len(findings) != 0) || (want > 0 && len(findings) < want) {
				t.Fatalf("findings: %v, want %d", findings, want)
			}
		})
	}
}
