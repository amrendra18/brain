package main

import "testing"

// This test input adds a discarded read before help dispatch in the real entrypoint.
func TestHelpReadObserverRejectsStartupRead(t *testing.T) {
	checkHelpVersionStateReads(t, true)
}
