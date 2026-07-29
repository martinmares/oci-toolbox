package cleanup

import (
	"testing"
	"time"
)

func TestBuildPlanProtectsLatestDigestAndInvalidTags(t *testing.T) {
	tags := []TagRecord{
		NewTagRecord("latest", "sha256:latest", ""),
		NewTagRecord("2024.01.05.1", "sha256:latest", ""),
		NewTagRecord("2024.01.04.1", "sha256:keep", ""),
		NewTagRecord("2024.01.03.1", "sha256:shared", ""),
		NewTagRecord("2024.01.02.1", "sha256:shared", ""),
		NewTagRecord("not-a-date", "sha256:invalid", ""),
	}

	plan := BuildPlan(tags, 2, true)

	if len(plan.Latest) != 1 || len(plan.Latest[0].Tags) != 2 {
		t.Fatalf("latest protected count = %d, want 2", len(plan.Latest))
	}
	if len(plan.Invalid) != 1 || plan.Invalid[0].Name != "not-a-date" {
		t.Fatalf("invalid = %#v", plan.Invalid)
	}
	if got := digests(plan.Keep); got != "sha256:keep,sha256:shared" {
		t.Fatalf("keep = %s", got)
	}
	if len(plan.Remove) != 0 {
		t.Fatalf("remove = %#v, want empty because keep_count counts unique digests", plan.Remove)
	}
}

func TestBuildPlanRemovesOlderUniqueDigests(t *testing.T) {
	tags := []TagRecord{
		NewTagRecord("2024.01.03.1", "sha256:a", ""),
		NewTagRecord("2024.01.02.1", "sha256:b", ""),
		NewTagRecord("2024.01.01.1", "sha256:c", ""),
	}

	plan := BuildPlan(tags, 1, true)

	if got := digests(plan.Keep); got != "sha256:a" {
		t.Fatalf("keep = %s", got)
	}
	if got := digests(plan.Remove); got != "sha256:b,sha256:c" {
		t.Fatalf("remove = %s", got)
	}
}

func TestBuildPlanCountsKeepCountByDigest(t *testing.T) {
	tags := []TagRecord{
		NewTagRecord("2026.06.11.0", "sha256:a", ""),
		NewTagRecord("2026.06.10.0", "sha256:b", ""),
		NewTagRecord("2026.06.09.0", "sha256:b", ""),
		NewTagRecord("2026.06.06.0", "sha256:c", ""),
		NewTagRecord("2026.06.05.2", "sha256:c", ""),
		NewTagRecord("2026.06.05.1", "sha256:c", ""),
	}

	plan := BuildPlan(tags, 2, true)

	if got := digests(plan.Keep); got != "sha256:a,sha256:b" {
		t.Fatalf("keep = %s", got)
	}
	if got := digests(plan.Remove); got != "sha256:c" {
		t.Fatalf("remove = %s", got)
	}
	if got := tagNames(plan.Remove[0].Tags); got != "2026.06.06.0,2026.06.05.2,2026.06.05.1" {
		t.Fatalf("remove tags = %s", got)
	}
}

func TestBuildCreatedPlanKeepsNewestDigestsByCreated(t *testing.T) {
	oldest := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	middle := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)

	tags := []TagRecord{
		{Name: "latest", Digest: "sha256:latest", Created: &newest},
		{Name: "release-protected", Digest: "sha256:protected", Created: &oldest, Protected: true},
		{Name: "tag-a", Digest: "sha256:a", Created: &oldest},
		{Name: "tag-b", Digest: "sha256:b", Created: &middle},
		{Name: "tag-c", Digest: "sha256:c", Created: &newest},
		{Name: "tag-empty-created", Digest: "sha256:empty"},
	}

	plan := BuildCreatedPlan(tags, 2, true)

	if got := digests(plan.Keep); got != "sha256:c,sha256:b" {
		t.Fatalf("keep = %s", got)
	}
	if got := digests(plan.Remove); got != "sha256:a" {
		t.Fatalf("remove = %s", got)
	}
	if len(plan.Latest) != 2 {
		t.Fatalf("protected groups = %d, want 2", len(plan.Latest))
	}
	if len(plan.Invalid) != 1 || plan.Invalid[0].Digest != "sha256:empty" {
		t.Fatalf("invalid = %#v", plan.Invalid)
	}
}

func digests(groups []DigestGroup) string {
	result := ""
	for i, group := range groups {
		if i > 0 {
			result += ","
		}
		result += group.Digest
	}
	return result
}

func tagNames(tags []TagRecord) string {
	result := ""
	for i, tag := range tags {
		if i > 0 {
			result += ","
		}
		result += tag.Name
	}
	return result
}
