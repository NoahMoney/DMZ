package xmrswap

import "strings"

// MT-L02: no secret reaches the companion's output. The state machine never logs one, and the crash fuzz checks it;
// the clients are the other way a secret could get there. A Claim carries s_a and k_b, a Refund s_b, and
// generate_from_keys the joint spend key and the wallet's password, each in a request whose answer can be an error,
// and an error is printed. A daemon, wallet or proxy that echoes the request into its error message would print the
// secret — and s_a in a log, once Bob's refund has put s_b on the chain, lets whoever reads the log sweep Alice's XMR.
// So each client withholds its own secrets from every error it returns.

// redactedError is an error whose text has had secrets withheld. It still unwraps to the original, so errors.Is and
// errors.As see what they saw before; only its text changes.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// withhold replaces every occurrence of each non-empty secret in err's text with "<label withheld>", pairwise.
func withhold(err error, secrets ...[2]string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	changed := false
	for _, s := range secrets {
		label, value := s[0], s[1]
		if value != "" && strings.Contains(msg, value) {
			msg = strings.ReplaceAll(msg, value, "<"+label+" withheld>")
			changed = true
		}
	}
	if !changed {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

// secretArgs are the contract arguments that are key material until the transaction carrying them is mined.
var secretArgs = map[string]bool{"sa": true, "kb": true, "sb": true}

// withholdArgs is withhold for the secret arguments of one contract call.
func withholdArgs(err error, args []Arg) error {
	var secrets [][2]string
	for _, a := range args {
		if v, ok := a.Value.(string); ok && secretArgs[a.Name] {
			secrets = append(secrets, [2]string{a.Name, v})
		}
	}
	return withhold(err, secrets...)
}
