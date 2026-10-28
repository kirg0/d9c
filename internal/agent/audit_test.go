package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewAuditEntry(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	rm := Op{Name: "rm", Tier: TierDestructive}
	action := ActionData{Action: "rm", Targets: []ActionTarget{
		{Ref: "web", ID: "9ae942fd8fbc", Name: "web", Result: outcomeOK},
		{Ref: "ghost", Result: outcomeFailed},
	}}
	tests := []struct {
		name string
		args Args
		call Call
		res  Result
		err  error
		want AuditEntry
	}{
		{"done with resolved targets", Args{"targets": []string{"web", "ghost"}}, Call{Confirmed: true},
			Result{Data: action, Partial: true}, nil,
			AuditEntry{Result: AuditPartial, Yes: true, Targets: []string{"web (9ae942fd8fbc)", "ghost: failed"}}},
		{"refused by confirmation", Args{"filter": []string{"status=exited"}}, Call{}, Result{},
			&Error{Code: CodeConfirmationRequired, Message: "needs confirmation"},
			AuditEntry{Result: AuditError, Code: CodeConfirmationRequired, Message: "needs confirmation",
				Targets: []string{"filter status=exited"}}},
		{"dry run", Args{"targets": []string{"web"}}, Call{DryRun: true}, Result{Data: PlanData{}}, nil,
			AuditEntry{Result: AuditPlanned, DryRun: true, Targets: []string{"web"}}},
		{"plain error classified", Args{"targets": []string{"web"}}, Call{Confirmed: true}, Result{},
			errors.New("boom"), AuditEntry{Result: AuditError, Code: CodeInternal, Message: "boom", Yes: true,
				Targets: []string{"web"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewAuditEntry(now, "prod", rm, tt.args, tt.call, tt.res, tt.err)
			want := tt.want
			want.Time, want.Host, want.Op, want.Tier, want.Args = now.UTC(), "prod", "rm", TierDestructive, tt.args
			if !reflect.DeepEqual(got, want) {
				t.Errorf("entry = %+v\nwant    %+v", got, want)
			}
		})
	}
}

func TestAuditTargetsFromArgs(t *testing.T) {
	args := Args{"project": "webapp", "file": "b.tar.gz", "image": "", "signal": "KILL"}
	if got := auditTargets(args, Result{}); !reflect.DeepEqual(got, []string{"webapp", "b.tar.gz"}) {
		t.Errorf("targets = %q", got)
	}
	if got := auditTargets(Args{}, Result{}); got != nil {
		t.Errorf("no targets = %q, want nil", got)
	}
}

func TestAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", AuditFileName)
	for i := range 2 {
		l, err := OpenAuditLog(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Record(AuditEntry{Op: "stop", Host: "h", Result: AuditOK, Args: Args{"n": i}}); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log = %q, want two lines", raw)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatal(err)
	}
	if e["op"] != "stop" || e["result"] != "ok" || e["tier"] != "read" {
		t.Errorf("entry = %v", e)
	}

	// A directory where the file should be: the log cannot open, and the
	// error says that writes are refused.
	blocked := filepath.Join(t.TempDir(), AuditFileName)
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = OpenAuditLog(blocked)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeInternal || !strings.Contains(ae.Hint, "refused") {
		t.Errorf("err = %v", err)
	}
}
