package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The launch.bas drain was not an arithmetic mistake. buyto refunded a caller's overshoot at line 59 and only
// then checked min_out at line 60, and the ...To wrapper turned that ordering into a double payment: the
// entrypoint returned 1, refundto paid ASSETVALUE(q) — still the whole deposit, because the DVM never
// decrements Assets on a send — and the wrapper returned 0, so both transfers committed. 2,052,806 taken from a
// 2,150,000 pot on the real DVM.
//
// Nothing in the fuzz suite could see it, because it is not a property of the maths; it is a property of
// statement order. So this test reads control flow instead: from every external send, can execution still reach
// a non-zero RETURN? If it can, that value can be paid twice the moment the function gains a ...To wrapper.
// pool.bas AddLiquidity had the same shape and was safe only because no wrapper existed yet.
func TestNoExternalSendBeforeAFailureExit(t *testing.T) {
	// Helpers whose return value is a result rather than a revert. factory.bas reg returns 1 on SUCCESS and its
	// three callers read it as `IF reg(scid) == 0 THEN GOTO 900`, so its non-zero RETURN reverts nothing.
	allowed := map[string]string{
		"factory.bas:reg": "returns 1 on success; callers test `reg(scid) == 0` for failure",
	}
	files, err := filepath.Glob("../contracts/*.bas")
	if err != nil || len(files) == 0 {
		t.Fatalf("no contracts: %v", err)
	}
	num := regexp.MustCompile(`^(\d+) (.*)$`)
	goTo := regexp.MustCompile(`GOTO (\d+)`)
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(file)
		fn, body := "", map[int]string{}
		check := func() {
			if fn == "" || len(body) == 0 {
				return
			}
			nums := make([]int, 0, len(body))
			for n := range body {
				nums = append(nums, n)
			}
			sort.Ints(nums)
			next := map[int]int{}
			for i := 0; i+1 < len(nums); i++ {
				next[nums[i]] = nums[i+1]
			}
			succ := func(n int) []int {
				txt := strings.TrimSpace(body[n])
				var out []int
				for _, m := range goTo.FindAllStringSubmatch(txt, -1) {
					v, _ := strconv.Atoi(m[1])
					out = append(out, v)
				}
				// an unconditional GOTO or a RETURN ends the fall-through
				if !strings.HasPrefix(txt, "GOTO ") && !strings.HasPrefix(txt, "RETURN") {
					if v, ok := next[n]; ok {
						out = append(out, v)
					}
				}
				return out
			}
			fails := func(n int) bool {
				txt := strings.TrimSpace(body[n])
				if !strings.HasPrefix(txt, "RETURN ") {
					return false
				}
				return strings.TrimSpace(strings.TrimPrefix(txt, "RETURN")) != "0"
			}
			for _, n := range nums {
				if !strings.Contains(body[n], "SEND_ASSET_TO_ADDRESS") && !strings.Contains(body[n], "SEND_DERO_TO_ADDRESS") {
					continue
				}
				seen, stack := map[int]bool{}, succ(n)
				for len(stack) > 0 {
					c := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if seen[c] {
						continue
					}
					seen[c] = true
					if fails(c) {
						key := base + ":" + fn
						if why, ok := allowed[key]; ok {
							t.Logf("%s: send at %d reaches a non-zero RETURN at %d — allowed: %s", key, n, c, why)
							break
						}
						t.Errorf("%s %s: line %d sends value out and can still reach the non-zero RETURN at %d.\n"+
							"  %d %s\n"+
							"  A ...To wrapper commits on a zero return, so this amount is paid once here and again by the\n"+
							"  refund path. Record it and send it past every exit, as buyto and AddLiquidity now do.",
							base, fn, n, c, n, strings.TrimSpace(body[n]))
						break
					}
					stack = append(stack, succ(c)...)
				}
			}
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSuffix(l, "\r")
			if strings.HasPrefix(l, "Function ") {
				check()
				fn = strings.SplitN(strings.TrimPrefix(l, "Function "), "(", 2)[0]
				body = map[int]string{}
				continue
			}
			if m := num.FindStringSubmatch(l); m != nil && fn != "" {
				n, _ := strconv.Atoi(m[1])
				body[n] = m[2]
			}
		}
		check()
	}
}
