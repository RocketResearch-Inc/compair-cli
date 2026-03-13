package config

import "testing"

func TestDefaultProfilesUseHostedCloudApp(t *testing.T) {
	prof := defaultProfiles()
	if got := prof.Profiles["cloud"].APIBase; got != defaultCloudAPIBase {
		t.Fatalf("expected cloud api base %q, got %q", defaultCloudAPIBase, got)
	}
}

func TestEnsureDefaultProfilesMigratesLegacyCloudBase(t *testing.T) {
	prof := &Profiles{
		Default: "cloud",
		Profiles: map[string]Profile{
			"cloud": {APIBase: legacyCloudAPIBase},
		},
	}
	changed := ensureDefaultProfiles(prof)
	if !changed {
		t.Fatalf("expected migration to mark profile as changed")
	}
	if got := prof.Profiles["cloud"].APIBase; got != defaultCloudAPIBase {
		t.Fatalf("expected migrated cloud api base %q, got %q", defaultCloudAPIBase, got)
	}
	if got := prof.Profiles["local"].APIBase; got != defaultLocalAPIBase {
		t.Fatalf("expected local api base %q, got %q", defaultLocalAPIBase, got)
	}
}

func TestEnsureDefaultProfilesPreservesCustomCloudBase(t *testing.T) {
	custom := "https://staging.compair.sh/api"
	prof := &Profiles{
		Default: "cloud",
		Profiles: map[string]Profile{
			"cloud": {APIBase: custom},
			"local": {APIBase: defaultLocalAPIBase},
		},
	}
	changed := ensureDefaultProfiles(prof)
	if changed {
		t.Fatalf("expected no change for custom cloud api base")
	}
	if got := prof.Profiles["cloud"].APIBase; got != custom {
		t.Fatalf("expected custom cloud api base %q, got %q", custom, got)
	}
}
