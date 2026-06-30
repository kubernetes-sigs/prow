/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package blunderbuss

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/github"
)

type blameGetter interface {
	GetBlame(org, repo, ref, path string) ([]github.BlameRange, error)
}

type lineRange struct {
	Start int
	End   int
}

var hunkHeaderRegexp = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseDiffHunks(patch string) []lineRange {
	var ranges []lineRange
	for line := range strings.SplitSeq(patch, "\n") {
		matches := hunkHeaderRegexp.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		start, _ := strconv.Atoi(matches[1])
		count := 1
		if matches[2] != "" {
			count, _ = strconv.Atoi(matches[2])
		}
		if count == 0 {
			// Pure addition — examine context around insertion point
			contextStart := max(start-5, 1)
			ranges = append(ranges, lineRange{Start: contextStart, End: start})
		} else {
			ranges = append(ranges, lineRange{Start: start, End: start + count - 1})
		}
	}
	return ranges
}

func intersectBlameWithChanges(blameRanges []github.BlameRange, changedRanges []lineRange) map[string]authorStats {
	stats := make(map[string]authorStats)
	for _, br := range blameRanges {
		if br.AuthorLogin == "" {
			continue
		}
		for _, cr := range changedRanges {
			overlapStart := max(br.StartingLine, cr.Start)
			overlapEnd := min(br.EndingLine, cr.End)
			if overlapStart > overlapEnd {
				continue
			}
			lines := overlapEnd - overlapStart + 1
			existing := stats[br.AuthorLogin]
			existing.LineCount += lines
			if br.Date.After(existing.MostRecentDate) {
				existing.MostRecentDate = br.Date
			}
			stats[br.AuthorLogin] = existing
		}
	}
	return stats
}

type authorStats struct {
	LineCount      int
	MostRecentDate time.Time
}

// diverseFileSelection picks up to limit files, round-robin across parent
// directories so that blame scoring samples broadly across the PR.
func diverseFileSelection(files []github.PullRequestChange, limit int) []github.PullRequestChange {
	var eligible []github.PullRequestChange
	for _, f := range files {
		if f.Status != "removed" {
			eligible = append(eligible, f)
		}
	}
	if len(eligible) <= limit {
		return eligible
	}

	groups := make(map[string][]github.PullRequestChange)
	var groupOrder []string
	for _, f := range eligible {
		dir := fileDir(f.Filename)
		if _, exists := groups[dir]; !exists {
			groupOrder = append(groupOrder, dir)
		}
		groups[dir] = append(groups[dir], f)
	}

	var selected []github.PullRequestChange
	idx := make(map[string]int)
	for len(selected) < limit {
		added := false
		for _, dir := range groupOrder {
			if len(selected) >= limit {
				break
			}
			i := idx[dir]
			if i < len(groups[dir]) {
				selected = append(selected, groups[dir][i])
				idx[dir] = i + 1
				added = true
			}
		}
		if !added {
			break
		}
	}
	return selected
}

func fileDir(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

const (
	lineCountWeight = 10.0
	recencyWeight   = 5.0
	approverBonus   = 3.0
	reviewerBonus   = 2.0
	maxBlameFiles   = 20
)

type reviewerScorer struct {
	ghc       blameGetter
	org       string
	repo      string
	ref       string
	approvers sets.Set[string]
	reviewers sets.Set[string]
	now       time.Time
	log       *logrus.Entry
}

func (rs *reviewerScorer) scoreReviewers(
	files []github.PullRequestChange,
) (map[string]float64, error) {
	scores := make(map[string]float64)

	selected := diverseFileSelection(files, maxBlameFiles)
	if len(selected) < len(files) {
		rs.log.WithFields(logrus.Fields{
			"total_files":    len(files),
			"selected_files": len(selected),
		}).Info("Large PR: sampling diverse subset of files for blame scoring")
	}

	for _, file := range selected {
		blameRanges, err := rs.ghc.GetBlame(rs.org, rs.repo, rs.ref, file.Filename)
		if err != nil {
			rs.log.WithError(err).WithField("file", file.Filename).Warn("Failed to get blame data, skipping file")
			continue
		}

		changedRanges := parseDiffHunks(file.Patch)
		if len(changedRanges) == 0 {
			continue
		}

		fileStats := intersectBlameWithChanges(blameRanges, changedRanges)
		for author, stats := range fileStats {
			daysSince := rs.now.Sub(stats.MostRecentDate).Hours() / 24
			recencyScore := 1.0 / (1.0 + daysSince)
			scores[author] += float64(stats.LineCount)*lineCountWeight + recencyScore*recencyWeight
		}
	}

	for author := range scores {
		if rs.approvers.Has(author) {
			scores[author] += approverBonus
		} else if rs.reviewers.Has(author) {
			scores[author] += reviewerBonus
		}
	}

	return scores, nil
}

