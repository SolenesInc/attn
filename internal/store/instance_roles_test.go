package store

import "testing"

func TestStoreInstanceRoleAssignmentAndConditionalClear(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if got := s.GetInstanceRole("chief_of_staff"); got != "" {
		t.Fatalf("initial role = %q, want empty", got)
	}
	if err := s.SetInstanceRole("chief_of_staff", "session-a"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if got := s.GetInstanceRole("chief_of_staff"); got != "session-a" {
		t.Fatalf("role = %q, want session-a", got)
	}

	if err := s.SetInstanceRole("chief_of_staff", "session-b"); err != nil {
		t.Fatalf("transfer role: %v", err)
	}
	if err := s.ClearInstanceRole("chief_of_staff", "session-a"); err != nil {
		t.Fatalf("stale clear: %v", err)
	}
	if got := s.GetInstanceRole("chief_of_staff"); got != "session-b" {
		t.Fatalf("role after stale clear = %q, want session-b", got)
	}

	if err := s.ClearInstanceRole("chief_of_staff", "session-b"); err != nil {
		t.Fatalf("clear role: %v", err)
	}
	if got := s.GetInstanceRole("chief_of_staff"); got != "" {
		t.Fatalf("role after clear = %q, want empty", got)
	}
}
