package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestAIBOXExternalResourcePathIsConfined(t *testing.T) {
	prefix := "local://7/aibox-uat-soak/"
	cases := []struct {
		path string
		want bool
	}{
		{"local://7/aibox-uat-soak/reports/1.pdf", true},
		{"local://7/aibox-uat-soak/reports/1.mp4", true},
		{"local://7/aibox-uat-soak/", false},
		{"local://7/aibox-uat-soak/.", false}, // root disguised as a relative file
		{"local://7/aibox-uat-soak/../other", false},
		{"local://7/aibox-uat-soak/reports//1.pdf", false},
		{"local://7/aibox-uat-soak/%2e%2e/x", false},
		{"local://7/aibox-uat-soak/reports\\secret", false},
		{"local://7/private/test", false},
		{"file:///etc/passwd", false},
	}
	for _, tc := range cases {
		if got := safeAIBOXSourceResourcePath(prefix, tc.path); got != tc.want {
			t.Errorf("path %q: got %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestAIBOXExternalSourceFingerprintMustBeSHA256Hex(t *testing.T) {
	cases := []struct {
		hash string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789", true},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := validAIBOXSourceSHA256(tc.hash); got != tc.want {
			t.Errorf("hash length %d valid=%v, want %v", len(tc.hash), got, tc.want)
		}
	}
}

// The catalog's tenant-wide Resolve is deliberately insufficient for a
// source_ref in another KB. Only the explicit KB registration can authorize it.
type aiboxScopeCatalog struct {
	interfaces.ResourceCatalog
	allowKB  string
	tenantID uint64
	err      error
}

func (s *aiboxScopeCatalog) IsAIBOXSourceRegisteredForKB(
	_ context.Context, tenantID uint64, kbID, _ string,
) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return tenantID == s.tenantID && kbID == s.allowKB, nil
}

func TestAIBOXDerivedSourceRefMustBeRegisteredForTargetKB(t *testing.T) {
	ctx := context.Background()
	catalog := &aiboxScopeCatalog{allowKB: "kb-A", tenantID: 42}
	for _, tc := range []struct {
		name   string
		tenant uint64
		kb     string
		want   bool
	}{
		{"own KB", 42, "kb-A", true},
		{"same tenant foreign KB", 42, "kb-B", false},
		{"other tenant", 43, "kb-A", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := aiboxSourceRegisteredForKB(ctx, catalog, tc.tenant, tc.kb, "resource://test")
			if err != nil || got != tc.want {
				t.Fatalf("allowed=%v err=%v, want %v", got, err, tc.want)
			}
		})
	}
	// A catalog without this lookup must fail closed, not accept a handle.
	var unsupported interfaces.ResourceCatalog = &struct{ interfaces.ResourceCatalog }{}
	if got, err := aiboxSourceRegisteredForKB(ctx, unsupported, 42, "kb-A", "resource://test"); err == nil || got {
		t.Fatalf("unsupported catalog: allowed=%v err=%v", got, err)
	}
	catalog.err = fmt.Errorf("repository unavailable")
	if got, err := aiboxSourceRegisteredForKB(ctx, catalog, 42, "kb-A", "resource://test"); err == nil || got {
		t.Fatalf("backend unavailable: allowed=%v err=%v", got, err)
	}
}
