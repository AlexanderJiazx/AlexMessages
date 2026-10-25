package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// emergency is the forced command of the restricted emergency key
// (OTAUpdatePlan.md, D9 and step 3.4):
//
//	command="/home/alexander/alexmessage/bin/otactl emergency",no-port-forwarding,no-agent-forwarding,no-pty
//
// sshd puts what the client asked to run into $SSH_ORIGINAL_COMMAND. Exactly
// one word is accepted: halt (every pointer), rollback-embedded (every
// pointer in state update or halted; it still asks for yes on stdin) or
// status. Anything else is refused and logged, so the key can do nothing
// else.
func setupEmergency(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		a.via = "emergency"
		words := strings.Fields(a.getenv("SSH_ORIGINAL_COMMAND"))
		word := ""
		if len(words) == 1 {
			word = words[0]
		}
		switch word {
		case "halt":
			return a.runHalt(matchAll, "")
		case "rollback-embedded":
			return a.runRollback(matchAll, "every active update")
		case "status":
			return a.status()
		}
		asked := a.getenv("SSH_ORIGINAL_COMMAND")
		if len(asked) > 200 {
			asked = asked[:200]
		}
		if err := a.logAction("emergency-refused", map[string]any{"command": asked}); err != nil {
			fmt.Fprintf(a.stderr, "otactl: log/actions.log: %v\n", err)
		}
		return errors.New("this key runs only one of: halt, rollback-embedded, status")
	}
}
