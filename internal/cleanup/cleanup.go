package cleanup

import (
	"regexp"
	"sort"
	"strconv"
	"time"
)

const DefaultTagPattern = `^(\d+)\.(\d+)\.(\d+)\.(\d+)$`

type TagRecord struct {
	Name      string
	Digest    string
	Blobs     []BlobRecord
	Created   *time.Time
	Protected bool
	Valid     bool
	Year      int
	Month     int
	Day       int
	BuildNo   int
}

type BlobRecord struct {
	Digest string
	Size   int64
}

type DigestGroup struct {
	Digest  string
	Tags    []TagRecord
	Newest  TagRecord
	Created *time.Time
}

type Plan struct {
	Latest  []DigestGroup
	Invalid []TagRecord
	Keep    []DigestGroup
	Remove  []DigestGroup
}

func NewTagRecord(name, digest, pattern string) TagRecord {
	if pattern == "" {
		pattern = DefaultTagPattern
	}
	rec := TagRecord{Name: name, Digest: digest}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return rec
	}
	matches := re.FindStringSubmatch(name)
	if len(matches) != 5 {
		return rec
	}

	year, errY := strconv.Atoi(matches[1])
	month, errM := strconv.Atoi(matches[2])
	day, errD := strconv.Atoi(matches[3])
	buildNo, errB := strconv.Atoi(matches[4])
	if errY != nil || errM != nil || errD != nil || errB != nil {
		return rec
	}
	if !validDate(year, month, day) {
		return rec
	}

	rec.Valid = true
	rec.Year = year
	rec.Month = month
	rec.Day = day
	rec.BuildNo = buildNo
	return rec
}

func BuildPlan(tags []TagRecord, keepCount int, protectLatest bool) Plan {
	return BuildTimestampPlan(tags, keepCount, protectLatest)
}

func BuildTimestampPlan(tags []TagRecord, keepCount int, protectLatest bool) Plan {
	if keepCount < 1 {
		keepCount = 1
	}

	protectedDigests := protectedDigestSet(tags, protectLatest)
	plan := protectedPlan(tags, protectedDigests)

	groupsByDigest := map[string]*DigestGroup{}
	for _, tag := range tags {
		if protectedDigests[tag.Digest] {
			continue
		}
		if !tag.Valid {
			plan.Invalid = append(plan.Invalid, tag)
			continue
		}

		group, ok := groupsByDigest[tag.Digest]
		if !ok {
			group = &DigestGroup{Digest: tag.Digest, Newest: tag}
			groupsByDigest[tag.Digest] = group
		}
		group.Tags = append(group.Tags, tag)
		if newerTag(tag, group.Newest) {
			group.Newest = tag
		}
	}

	candidates := make([]DigestGroup, 0, len(groupsByDigest))
	for _, group := range groupsByDigest {
		sortTagRecords(group.Tags)
		candidates = append(candidates, *group)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return newerTag(candidates[i].Newest, candidates[j].Newest)
	})

	if keepCount > len(candidates) {
		keepCount = len(candidates)
	}
	plan.Keep = append(plan.Keep, candidates[:keepCount]...)
	plan.Remove = append(plan.Remove, candidates[keepCount:]...)

	return plan
}

func BuildCreatedPlan(tags []TagRecord, keepCount int, protectLatest bool) Plan {
	if keepCount < 1 {
		keepCount = 1
	}

	protectedDigests := protectedDigestSet(tags, protectLatest)
	plan := protectedPlan(tags, protectedDigests)

	groupsByDigest := map[string]*DigestGroup{}
	for _, tag := range tags {
		if protectedDigests[tag.Digest] {
			continue
		}
		if tag.Created == nil {
			plan.Invalid = append(plan.Invalid, tag)
			continue
		}

		group, ok := groupsByDigest[tag.Digest]
		if !ok {
			group = &DigestGroup{Digest: tag.Digest, Newest: tag, Created: tag.Created}
			groupsByDigest[tag.Digest] = group
		}
		group.Tags = append(group.Tags, tag)
		if newerTag(tag, group.Newest) {
			group.Newest = tag
		}
		if group.Created == nil || tag.Created.After(*group.Created) {
			group.Created = tag.Created
		}
	}

	candidates := make([]DigestGroup, 0, len(groupsByDigest))
	for _, group := range groupsByDigest {
		sortTagRecords(group.Tags)
		candidates = append(candidates, *group)
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.Created == nil && right.Created == nil {
			return left.Digest > right.Digest
		}
		if left.Created == nil {
			return false
		}
		if right.Created == nil {
			return true
		}
		if !left.Created.Equal(*right.Created) {
			return left.Created.After(*right.Created)
		}
		return left.Digest > right.Digest
	})

	if keepCount > len(candidates) {
		keepCount = len(candidates)
	}
	plan.Keep = append(plan.Keep, candidates[:keepCount]...)
	plan.Remove = append(plan.Remove, candidates[keepCount:]...)

	return plan
}

func protectedDigestSet(tags []TagRecord, protectLatest bool) map[string]bool {
	protectedDigests := map[string]bool{}
	if protectLatest {
		for _, tag := range tags {
			if tag.Name == "latest" {
				protectedDigests[tag.Digest] = true
			}
		}
	}
	for _, tag := range tags {
		if tag.Protected {
			protectedDigests[tag.Digest] = true
		}
	}
	return protectedDigests
}

func protectedPlan(tags []TagRecord, protectedDigests map[string]bool) Plan {
	plan := Plan{}
	protectedDigestList := make([]string, 0, len(protectedDigests))
	for digest := range protectedDigests {
		protectedDigestList = append(protectedDigestList, digest)
	}
	sort.Strings(protectedDigestList)
	for _, digest := range protectedDigestList {
		protectedTags := tagsByDigest(tags, digest)
		if len(protectedTags) == 0 {
			continue
		}
		sortTagRecords(protectedTags)
		plan.Latest = append(plan.Latest, DigestGroup{
			Digest:  digest,
			Tags:    protectedTags,
			Newest:  protectedTags[0],
			Created: newestCreated(protectedTags),
		})
	}
	return plan
}

func tagsByDigest(tags []TagRecord, digest string) []TagRecord {
	result := make([]TagRecord, 0)
	for _, tag := range tags {
		if tag.Digest == digest {
			result = append(result, tag)
		}
	}
	return result
}

func sortTagRecords(tags []TagRecord) {
	sort.Slice(tags, func(i, j int) bool {
		left := tags[i]
		right := tags[j]
		if left.Valid != right.Valid {
			return left.Valid
		}
		if left.Valid && right.Valid {
			return newerTag(left, right)
		}
		return left.Name < right.Name
	})
}

func newerTag(left, right TagRecord) bool {
	if left.Year != right.Year {
		return left.Year > right.Year
	}
	if left.Month != right.Month {
		return left.Month > right.Month
	}
	if left.Day != right.Day {
		return left.Day > right.Day
	}
	if left.BuildNo != right.BuildNo {
		return left.BuildNo > right.BuildNo
	}
	return left.Name > right.Name
}

func newestCreated(tags []TagRecord) *time.Time {
	var newest *time.Time
	for _, tag := range tags {
		if tag.Created == nil {
			continue
		}
		if newest == nil || tag.Created.After(*newest) {
			newest = tag.Created
		}
	}
	return newest
}

func validDate(year, month, day int) bool {
	if year < 1970 {
		return false
	}
	dt := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return dt.Year() == year && int(dt.Month()) == month && dt.Day() == day
}
