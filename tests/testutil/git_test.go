package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryFixture(t *testing.T) {
	project := t.TempDir()
	Repository(t, project, "")
	Repository(t, project, "An owned fixture commit")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git unavailable; fixture creation still ran")
	}
	git, err = filepath.Abs(git)
	if err != nil {
		t.Fatal(err)
	}
	read := func() {
		t.Helper()
		command := exec.Command(git, "--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-C", project, "log", "-1", "--no-show-signature", "--format=%cI%n%s")
		environment := []string{}
		for _, value := range command.Environ() {
			if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
				environment = append(environment, value)
			}
		}
		command.Env = append(environment, "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=")
		output, err := command.Output()
		if err != nil || string(output) != "2025-10-12T22:13:20+02:00\nAn owned fixture commit\n" {
			t.Fatal("the fixture is not readable by real Git", err, string(output))
		}
	}
	read()
	Repository(t, project, "An owned fixture commit", strings.Join([]string{"-----BEGIN PGP SIGNATURE-----", "owned fixture signature", "-----END PGP SIGNATURE-----"}, "\n"))
	read()
}
