package features

import (
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

const testRegistry = "registry.example.test/collection"

// ociNode builds an OCI feature node. source is relative to testRegistry,
// e.g. "alpha:1".
func ociNode(t *testing.T, source string, after ...string) *node {
	t.Helper()
	full := testRegistry + "/" + source
	ref, err := oci.ParseReference(full)
	if err != nil {
		t.Fatal(err)
	}
	id := ref.Repository[strings.LastIndex(ref.Repository, "/")+1:]
	return &node{
		res: Resolved{
			Source:    full,
			Reference: &ref,
			Digest:    "sha256:" + id + "-" + ref.Tag,
			Metadata:  Metadata{ID: id, InstallsAfter: after},
		},
		options: "{}",
	}
}

func localNode(path, id string, after ...string) *node {
	return &node{
		res: Resolved{
			Source:    path,
			localPath: path,
			Metadata:  Metadata{ID: id, InstallsAfter: after},
		},
		options: "{}",
	}
}

func ids(rs []Resolved) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Metadata.ID
	}
	return out
}

func mustOrder(t *testing.T, nodes []*node, override ...string) []string {
	t.Helper()
	out, err := installOrder(nodes, override, "/base")
	if err != nil {
		t.Fatalf("installOrder: %v", err)
	}
	return ids(out)
}

func assertOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestInstallOrder_UntaggedInstallsAfterMatchesTaggedFeature(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "beta:2", testRegistry+"/alpha"),
		ociNode(t, "alpha:1"),
	})
	assertOrder(t, got, "alpha", "beta")
}

func TestInstallOrder_InstallsAfterIgnoresTagAndDigest(t *testing.T) {
	for _, after := range []string{
		testRegistry + "/alpha:9",
		testRegistry + "/alpha@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	} {
		t.Run(after, func(t *testing.T) {
			got := mustOrder(t, []*node{
				ociNode(t, "beta:2", after),
				ociNode(t, "alpha:1"),
			})
			assertOrder(t, got, "alpha", "beta")
		})
	}
}

func TestInstallOrder_InstallsAfterHonorsLegacyIDs(t *testing.T) {
	alpha := ociNode(t, "alpha:1")
	alpha.res.Metadata.LegacyIDs = []string{"old-alpha"}
	got := mustOrder(t, []*node{
		ociNode(t, "beta:2", testRegistry+"/old-alpha"),
		alpha,
	})
	assertOrder(t, got, "alpha", "beta")
}

func TestInstallOrder_InstallsAfterOtherRegistryDoesNotMatch(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:1", "other.example.test/collection/beta"),
		ociNode(t, "beta:1"),
	})
	// No edge, so both install in the first round, sorted by id.
	assertOrder(t, got, "alpha", "beta")
}

func TestInstallOrder_UnknownAndLegacyInstallsAfterAreIgnored(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "beta:1", testRegistry+"/missing", "bare-id"),
		ociNode(t, "alpha:1"),
	})
	assertOrder(t, got, "alpha", "beta")
}

func TestInstallOrder_RoundIsSortedByID(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "gamma:1"),
		ociNode(t, "alpha:1"),
		ociNode(t, "beta:1"),
	})
	assertOrder(t, got, "alpha", "beta", "gamma")
}

func TestInstallOrder_ChainAcrossRounds(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:1", testRegistry+"/beta"),
		ociNode(t, "beta:1", testRegistry+"/gamma"),
		ociNode(t, "gamma:1"),
	})
	assertOrder(t, got, "gamma", "beta", "alpha")
}

func TestInstallOrder_OverrideSetsPriority(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:1"),
		ociNode(t, "beta:1"),
		ociNode(t, "gamma:1"),
	}, testRegistry+"/gamma", testRegistry+"/alpha:1")
	assertOrder(t, got, "gamma", "alpha", "beta")
}

func TestInstallOrder_OverrideDoesNotBreakInstallsAfter(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:1"),
		ociNode(t, "gamma:1", testRegistry+"/alpha"),
	}, testRegistry+"/gamma")
	assertOrder(t, got, "alpha", "gamma")
}

func TestInstallOrder_OverrideUnparseableIsError(t *testing.T) {
	_, err := installOrder([]*node{ociNode(t, "alpha:1")}, []string{"bare-id"}, "/base")
	if err == nil || !strings.Contains(err.Error(), "overrideFeatureInstallOrder") {
		t.Fatalf("expected override error, got %v", err)
	}
}

func TestInstallOrder_InstallsAfterCycleIsError(t *testing.T) {
	_, err := installOrder([]*node{
		ociNode(t, "alpha:1", testRegistry+"/beta"),
		ociNode(t, "beta:1", testRegistry+"/alpha"),
	}, nil, "/base")
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestInstallOrder_LocalInstallsAfterResolvesRelativeToBaseDir(t *testing.T) {
	got := mustOrder(t, []*node{
		localNode("/base/beta", "beta", "./alpha"),
		localNode("/base/alpha", "alpha"),
	})
	assertOrder(t, got, "alpha", "beta")
}

func TestInstallOrder_SameResourceSortsByTag(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:2"),
		ociNode(t, "alpha:1"),
	})
	assertOrder(t, got, "alpha", "alpha")
	out, _ := installOrder([]*node{ociNode(t, "alpha:2"), ociNode(t, "alpha:1")}, nil, "/base")
	if out[0].Reference.Tag != "1" {
		t.Fatalf("first tag = %q, want 1", out[0].Reference.Tag)
	}
}
