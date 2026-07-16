package worker

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBuildDropletActionRequestCannotOverrideValidatedAction(t *testing.T) {
	parameters := map[string]any{
		"type": "rebuild",
		"size": "s-2vcpu-4gb",
	}

	request := buildDropletActionRequest("resize", parameters, 42, time.Time{})

	if got := request["type"]; got != "resize" {
		t.Fatalf("request type = %#v, want resize", got)
	}
	if got := request["size"]; got != "s-2vcpu-4gb" {
		t.Fatalf("request size = %#v", got)
	}
	if parameters["type"] != "rebuild" {
		t.Fatal("input parameters were mutated")
	}
}

func TestBuildDropletActionRequestAddsStableSnapshotName(t *testing.T) {
	now := time.Date(2026, time.July, 17, 9, 8, 7, 0, time.FixedZone("UTC+8", 8*60*60))
	request := buildDropletActionRequest("snapshot", nil, 1234, now)

	if got := request["name"]; got != "snapshot-1234-20260717-010807" {
		t.Fatalf("snapshot name = %#v", got)
	}

	request = buildDropletActionRequest("snapshot", map[string]any{"name": "manual-name"}, 1234, now)
	if got := request["name"]; got != "manual-name" {
		t.Fatalf("provided snapshot name = %#v", got)
	}
}

func TestPersistDropletProjectsAttemptsEveryCreatedDroplet(t *testing.T) {
	var calls []int64
	failures := persistDropletProjects([]int64{11, 22, 33}, "project-abc", func(providerID int64, projectID string) error {
		if projectID != "project-abc" {
			t.Fatalf("project ID = %q", projectID)
		}
		calls = append(calls, providerID)
		if providerID == 22 {
			return errors.New("write failed")
		}
		return nil
	})

	if !reflect.DeepEqual(calls, []int64{11, 22, 33}) {
		t.Fatalf("persist calls = %#v", calls)
	}
	if len(failures) != 1 || failures[0]["droplet_id"] != int64(22) || failures[0]["project_id"] != "project-abc" {
		t.Fatalf("failures = %#v", failures)
	}
}

func TestValidDropletIDs(t *testing.T) {
	if !validDropletIDs([]int64{1, 2, 3}, 3) {
		t.Fatal("valid droplet IDs were rejected")
	}
	for _, ids := range [][]int64{nil, {}, {0}, {-1}, {1, 1}, {1, 2, 3, 4}} {
		if validDropletIDs(ids, 3) {
			t.Fatalf("invalid droplet IDs were accepted: %#v", ids)
		}
	}
}
