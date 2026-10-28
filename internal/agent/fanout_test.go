package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFanoutResult(t *testing.T) {
	ok := func(h string) HostResult { return HostResult{Host: h, Runtime: "docker", Data: []int{1}} }
	bad := func(h string, c Code) HostResult {
		return HostResult{Host: h, Error: &Error{Code: c, Message: "down", Hint: "hint " + h}}
	}

	res, err := FanoutResult("ps", []HostResult{ok("a"), ok("b")})
	if err != nil || res.Partial || res.Truncated || res.Hint != "" || len(res.Data.([]HostResult)) != 2 {
		t.Errorf("all ok = %+v, %v", res, err)
	}

	cut := ok("b")
	cut.Truncated = true
	res, err = FanoutResult("ps", []HostResult{ok("a"), cut})
	if err != nil || !res.Truncated || !strings.Contains(res.Hint, "data[].hint") {
		t.Errorf("truncated = %+v, %v", res, err)
	}

	res, err = FanoutResult("ps", []HostResult{ok("a"), bad("b", CodeConnection), ok("c")})
	if err != nil || !res.Partial || res.Hint != "1 of 3 hosts failed; see data[].error" {
		t.Errorf("partial = %+v, %v", res, err)
	}

	_, err = FanoutResult("ps", []HostResult{bad("a", CodeAuthRequired)})
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeAuthRequired || ae.Message != "a: down" || ae.Hint != "hint a" {
		t.Errorf("single failure = %v", err)
	}

	var all []HostResult
	for i := range 7 {
		all = append(all, bad(fmt.Sprintf("h%d", i), CodeNotFound))
	}
	all[0].Error.Code = CodeConnection
	_, err = FanoutResult("info", all)
	if !errors.As(err, &ae) || ae.Code != CodeConnection ||
		!strings.HasPrefix(ae.Message, "info failed on all 7 hosts: h0: down; h1: down") ||
		!strings.HasSuffix(ae.Message, "h4: down; and 2 more") {
		t.Errorf("all failed = %v", err)
	}
}
