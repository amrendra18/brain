package platform

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNextPerShell(t *testing.T) {
	for _, test := range []struct {
		shell string
		argv  []string
		want  string
	}{
		{"sh", []string{"/srv/two spaces/brain", `café "quote" $cash`, "a\\"}, `"/srv/two spaces/brain" "café \"quote\" \$cash" "a\\"`},
		{"powershell", []string{`C:\two spaces\brain.exe`, "keeper’s 'boat'", `a\`}, `& 'C:\two spaces\brain.exe' 'keeper’’s ''boat''' 'a\'`},
		{"sh", []string{"/srv/brain", "`tick` %NAME% !NAME!"}, `"/srv/brain" "\` + "`" + `tick\` + "`" + ` %NAME% !NAME!"`},
	} {
		if got := Command(test.shell, test.argv); got != test.want {
			t.Fatal(test.shell, got, test.want)
		}
	}
	argv := []string{`C:\two spaces\brain.exe`, `café "boat"`, "%NAME%", "!NAME!", `end\`, "keeper’s"}
	cmd := Command("cmd", argv)
	encoded := strings.Split(cmd, "-EncodedCommand ")
	if len(encoded) != 2 {
		t.Fatal(cmd)
	}
	data, err := base64.StdEncoding.DecodeString(encoded[1])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[2*i:])
	}
	script := string(utf16.Decode(units))
	if script != Command("powershell", argv) || !strings.Contains(script, "%NAME%") || !strings.Contains(script, "!NAME!") || !strings.Contains(script, `\"boat\"`) || !strings.Contains(script, `end\\"`) {
		t.Fatal(script)
	}
	if Command("sh", nil) != "" || nativePowerShell(nil) != "" {
		t.Fatal("empty argv")
	}
	for _, test := range []struct{ input, want string }{{"", `""`}, {`\"`, `"\\\""`}, {`two\\x`, `"two\\x"`}, {"alpha", `"alpha"`}} {
		if got := windowsArg(test.input); got != test.want {
			t.Fatal(test.input, got, test.want)
		}
	}
}

func TestWindowsPathRules(t *testing.T) {
	for _, name := range []string{"con.txt", "COM¹", "LPT².docx", "conout$", "NUL:stream"} {
		if !ReservedName(name) {
			t.Fatal(name)
		}
	}
	for _, root := range []string{`C:\Mixed Case\State`, `C:/Mixed Case/State`, `\\server\share\State`} {
		got, err := StateFolder("windows", "", nil, root)
		if err != nil || got != root {
			t.Fatal(root, got, err)
		}
	}
	for _, root := range []string{`C:relative`, `\relative`, `relative\State`} {
		if _, err := StateFolder("windows", "", nil, root); err == nil {
			t.Fatal(root)
		}
	}
	got, err := StateFolder("windows", `C:\Mixed Case`, nil, "")
	if err != nil || got != `C:\Mixed Case\AppData\Local\Brain` {
		t.Fatal(got, err)
	}
}
