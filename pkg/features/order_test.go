package features

import (
	"errors"
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
		options:      "{}",
		optionsValue: map[string]any{},
	}
}

func localNode(path, id string, after ...string) *node {
	return &node{
		res: Resolved{
			Source:    path,
			localPath: path,
			Metadata:  Metadata{ID: id, InstallsAfter: after},
		},
		options:      "{}",
		optionsValue: map[string]any{},
	}
}

func ids(rs []Resolved) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Metadata.ID
	}
	return out
}

// noLookup fails the test if an alias lookup happens.
func noLookup(t *testing.T) aliasLookup {
	return func(id string) ([]string, error) {
		t.Errorf("unexpected alias lookup for %s", id)
		return nil, errors.New("unexpected lookup")
	}
}

func mustOrder(t *testing.T, nodes []*node, override ...string) []string {
	t.Helper()
	out, err := installOrder(nodes, override, "/base", noLookup(t))
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

// The dependency sorts after its dependent by id, so only the edge can
// put it first.
func TestInstallOrder_UntaggedInstallsAfterMatchesTaggedFeature(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:2", testRegistry+"/beta"),
		ociNode(t, "beta:1"),
	})
	assertOrder(t, got, "beta", "alpha")
}

func TestInstallOrder_InstallsAfterIgnoresTagAndDigest(t *testing.T) {
	for _, after := range []string{
		testRegistry + "/beta:9",
		testRegistry + "/beta@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	} {
		t.Run(after, func(t *testing.T) {
			got := mustOrder(t, []*node{
				ociNode(t, "alpha:2", after),
				ociNode(t, "beta:1"),
			})
			assertOrder(t, got, "beta", "alpha")
		})
	}
}

// Upstream reads aliases from the referenced soft dependency's own
// metadata: alpha installsAfter beta, beta declares legacyIds
// [old-beta], and the installed feature is old-beta (whose metadata
// predates the rename).
func TestInstallOrder_InstallsAfterMatchesThroughReferencedAliases(t *testing.T) {
	var looked []string
	lookup := func(id string) ([]string, error) {
		looked = append(looked, id)
		return []string{"beta", "old-beta"}, nil
	}
	out, err := installOrder([]*node{
		ociNode(t, "alpha:2", testRegistry+"/beta"),
		ociNode(t, "old-beta:1"),
	}, nil, "/base", lookup)
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, ids(out), "old-beta", "alpha")
	if strings.Join(looked, ",") != testRegistry+"/beta" {
		t.Fatalf("lookups = %v", looked)
	}
}

// The installed feature's own legacyIds do not satisfy a reference to
// another name; only the referenced feature's aliases do.
func TestInstallOrder_InstalledLegacyIDsDoNotMatch(t *testing.T) {
	beta := ociNode(t, "beta:1")
	beta.res.Metadata.LegacyIDs = []string{"old-beta"}
	out, err := installOrder([]*node{
		ociNode(t, "alpha:2", testRegistry+"/old-beta"),
		beta,
	}, nil, "/base", func(string) ([]string, error) { return []string{"old-beta"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	// No edge: both install in the first round, sorted by id.
	assertOrder(t, ids(out), "alpha", "beta")
}

func TestInstallOrder_AliasLookupFailureIsError(t *testing.T) {
	_, err := installOrder([]*node{
		ociNode(t, "alpha:2", testRegistry+"/beta"),
		ociNode(t, "old-beta:1"),
	}, nil, "/base", func(string) ([]string, error) { return nil, errors.New("registry down") })
	if err == nil || !strings.Contains(err.Error(), "registry down") {
		t.Fatalf("expected lookup error, got %v", err)
	}
}

// Override entries carry no aliases, as upstream.
func TestInstallOrder_OverrideMatchesResourceOnly(t *testing.T) {
	beta := ociNode(t, "beta:1")
	beta.res.Metadata.LegacyIDs = []string{"old-beta"}
	got := mustOrder(t, []*node{ociNode(t, "alpha:1"), beta}, testRegistry+"/old-beta")
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
	out, err := installOrder([]*node{
		ociNode(t, "beta:1", testRegistry+"/missing", "bare-id"),
		ociNode(t, "alpha:1"),
	}, nil, "/base", func(id string) ([]string, error) {
		if id != testRegistry+"/missing" {
			t.Errorf("unexpected lookup %s", id)
		}
		return []string{"missing"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOrder(t, ids(out), "alpha", "beta")
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
		ociNode(t, "alpha:1", testRegistry+"/gamma"),
		ociNode(t, "gamma:1"),
	}, testRegistry+"/alpha")
	assertOrder(t, got, "gamma", "alpha")
}

func TestInstallOrder_OverrideUnparseableIsError(t *testing.T) {
	_, err := installOrder([]*node{ociNode(t, "alpha:1")}, []string{"bare-id"}, "/base", noLookup(t))
	if err == nil || !strings.Contains(err.Error(), "overrideFeatureInstallOrder") {
		t.Fatalf("expected override error, got %v", err)
	}
}

func TestInstallOrder_InstallsAfterCycleIsError(t *testing.T) {
	_, err := installOrder([]*node{
		ociNode(t, "alpha:1", testRegistry+"/beta"),
		ociNode(t, "beta:1", testRegistry+"/alpha"),
	}, nil, "/base", noLookup(t))
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestInstallOrder_LocalInstallsAfterResolvesRelativeToBaseDir(t *testing.T) {
	got := mustOrder(t, []*node{
		localNode("/base/alpha", "alpha", "./beta"),
		localNode("/base/beta", "beta"),
	})
	assertOrder(t, got, "beta", "alpha")
}

func TestInstallOrder_SameResourceSortsByTag(t *testing.T) {
	got := mustOrder(t, []*node{
		ociNode(t, "alpha:2"),
		ociNode(t, "alpha:1"),
	})
	assertOrder(t, got, "alpha", "alpha")
	out, _ := installOrder([]*node{ociNode(t, "alpha:2"), ociNode(t, "alpha:1")}, nil, "/base", noLookup(t))
	if out[0].Reference.Tag != "1" {
		t.Fatalf("first tag = %q, want 1", out[0].Reference.Tag)
	}
}

func TestCompareNodes_NamespaceThenDigest(t *testing.T) {
	other, err := oci.ParseReference("registry.example.test/another/zeta:1")
	if err != nil {
		t.Fatal(err)
	}
	a := &node{res: Resolved{Source: "registry.example.test/another/zeta:1", Reference: &other, Digest: "sha256:z", Metadata: Metadata{ID: "zeta"}}}
	if compareNodes(a, ociNode(t, "alpha:1")) >= 0 {
		t.Fatal("namespace must sort before id")
	}
	x, y := ociNode(t, "alpha:1"), ociNode(t, "alpha:1")
	x.res.Digest, y.res.Digest = "sha256:1", "sha256:2"
	if compareNodes(x, y) >= 0 || compareNodes(y, x) <= 0 {
		t.Fatal("same feature, tag and options must sort by digest")
	}
	y.res.Digest = x.res.Digest
	if !x.equals(y) {
		t.Fatal("same digest and options must be equal")
	}
}
