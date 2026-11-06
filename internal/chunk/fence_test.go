package chunk

import "testing"

func TestFence(t *testing.T) {
	for _, test := range []struct {
		name  string
		lines []string
		want  []bool
	}{
		{"mixed", []string{"```md", "~~~", "# Example", "```", "prose"}, []bool{true, true, true, true, false}},
		{"long", []string{"````", "```", "# Example", "`````", "prose"}, []bool{true, true, true, true, false}},
		{"closing text", []string{"~~~md", "~~~ trailing", "# Example", "~~~ \t\r", "prose"}, []bool{true, true, true, true, false}},
		{"indentation", []string{"   ```", "", "    ```", "# Example", "   ```", "prose"}, []bool{true, true, true, true, true, false}},
		{"invalid openers", []string{"    ```", "\t~~~", "``", "```lang`bad", "# Real", ""}, []bool{false, false, false, false, false, false}},
		{"tilde info", []string{"~~~lang`allowed", "~~~", "prose"}, []bool{true, true, false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fence Fence
			for i, line := range test.lines {
				if got := fence.Consume(line); got != test.want[i] {
					t.Fatalf("line %d %q: got %t, want %t", i, line, got, test.want[i])
				}
			}
		})
	}
}
