package ui

import "testing"

// TestAccountFingerprint: the fingerprint of claude auth status (claude 2.1.302's output) changes with the account,
// not with what else the status prints, never holds the account's name, and is empty when signed out.
func TestAccountFingerprint(t *testing.T) {
	t.Parallel()
	status := func(email, analytics string) []byte {
		return []byte(`{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "analyticsDisabled": ` +
			analytics + `, "configDirectory": "/home/me/.claude", "email": "` + email + `", "orgId": "d602a714", ` +
			`"orgName": "Acme", "subscriptionType": "team"}`)
	}
	one := accountFingerprint(status("one@example.com", "false"))
	if one == "" || len(one) != 16 {
		t.Fatalf("fingerprint %q, want 16 hex digits", one)
	}
	if again := accountFingerprint(status("one@example.com", "true")); again != one {
		t.Errorf("same account, another setting: %q, want %q", again, one)
	}
	if other := accountFingerprint(status("two@example.com", "false")); other == one {
		t.Errorf("another account, the same fingerprint %q", other)
	}
	if out := accountFingerprint([]byte(`{"loggedIn": false}`)); out != "" {
		t.Errorf("signed out: %q, want none", out)
	}
	if codex := accountFingerprint([]byte("Logged in using ChatGPT\n")); codex == "" {
		t.Error("codex's line: no fingerprint")
	}
	if none := accountFingerprint(nil); none != "" {
		t.Errorf("nothing printed: %q", none)
	}
}
