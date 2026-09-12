package main

import "testing"

// TestTheResumeCommandIsTheCommandLine (MT-B15): the command the recovery card prints and the wake runs is this
// companion's own executable with the same arguments, quoted where a shell would split them, and nothing else.
func TestTheResumeCommandIsTheCommandLine(t *testing.T) {
	got := resumeCommand(`D:\tools\xmrswap.exe`, []string{"alice", "-state", `swap dir\alice.json`, "-net", "stagenet", "-dero", "127.0.0.1:30007", "-monero-name", ""})
	want := `D:\tools\xmrswap.exe alice -state "swap dir\alice.json" -net stagenet -dero 127.0.0.1:30007 -monero-name ""`
	if got != want {
		t.Fatalf("resume command\n got  %s\n want %s", got, want)
	}
}
