package platform

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivateDarwinACL(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := PrepareState(state); err != nil {
		t.Fatal("private state control", err)
	}
	path := filepath.Join(state, "values.json")
	if err := os.WriteFile(path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateFile(state, "values.json", 100); err != nil {
		t.Fatal("private file control", err)
	}
	for _, name := range []string{path, state} {
		command := exec.Command("/bin/chmod", "+a", "everyone allow read", name)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(string(output), err)
		}
		if name == path {
			if _, err := ReadPrivateFile(state, "values.json", 100); !errors.Is(err, ErrStateUnsafe) {
				t.Fatal("public ACL file accepted", err)
			}
			if err := PreparePrivateFile(state, "values.json", false); !errors.Is(err, ErrStateUnsafe) {
				t.Fatal("public ACL file prepared", err)
			}
		} else if err := CheckPrivateState(state); !errors.Is(err, ErrStateUnsafe) {
			t.Fatal("public ACL state accepted", err)
		}
		if output, err := exec.Command("/bin/chmod", "-N", name).CombinedOutput(); err != nil {
			t.Fatal(string(output), err)
		}
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone deny delete", path).CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	t.Cleanup(func() { exec.Command("/bin/chmod", "-N", path).Run() })
	if _, err := ReadPrivateFile(state, "values.json", 100); err != nil {
		t.Fatal("deny-only ACL rejected", err)
	}
}

func TestPrivateACLBuffer(t *testing.T) {
	valid := make([]byte, 80)
	binary.LittleEndian.PutUint32(valid, 80)
	binary.LittleEndian.PutUint32(valid[4:], 8)
	binary.LittleEndian.PutUint32(valid[8:], 68)
	binary.LittleEndian.PutUint32(valid[12:], 0x012cc16d)
	binary.LittleEndian.PutUint32(valid[48:], 1)
	binary.LittleEndian.PutUint32(valid[72:], 2)
	if err := checkPrivateACL(valid); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		offset int
		value  uint32
	}{{0, 4}, {4, 0xffffffff}, {8, 4096}, {12, 0}, {48, 129}, {72, 1}} {
		data := append([]byte{}, valid...)
		binary.LittleEndian.PutUint32(data[test.offset:], test.value)
		if err := checkPrivateACL(data); !errors.Is(err, ErrStateUnsafe) {
			t.Fatal(test, err)
		}
	}
	if err := checkPrivateACL(nil); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(valid[48:], 0xffffffff)
	if err := checkPrivateACL(valid); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(t.TempDir(), "missing"))
	if file != nil || err == nil {
		t.Fatal(err)
	}
	file, err = os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := privateACL(file); !errors.Is(err, ErrStateUnsafe) {
		t.Fatal(err)
	}
}
