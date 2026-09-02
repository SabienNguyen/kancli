package board

import (
	"sort"
	"strings"
	"unicode"

	"github.com/sahilm/fuzzy"
)

// relevance scores how well a task matches the free words of a query.
// Structured terms (labels, due, ...) have already filtered the task; the
// score only orders the survivors. Title hits weigh most, then labels and
// assignee, then description, then checklist and comments, with a fuzzy
// title match as a tie breaker.
func Relevance(q Query, t Task) int {
	if len(q.Words) == 0 {
		return 0
	}
	title := strings.ToLower(t.Title)
	desc := strings.ToLower(t.Description)
	labels := strings.ToLower(strings.Join(t.Labels, " "))
	who := strings.ToLower(t.Assignee)
	extra := strings.ToLower(strings.Join(commentTexts(t), "\n"))
	score := 0
	for _, w := range q.Words {
		switch {
		case title == w:
			score += 100
		case strings.HasPrefix(title, w):
			score += 60
		case containsWord(title, w):
			score += 40
		case strings.Contains(title, w):
			score += 25
		}
		if strings.Contains(labels, w) || strings.Contains(who, w) {
			score += 20
		}
		if containsWord(desc, w) {
			score += 12
		} else if strings.Contains(desc, w) {
			score += 6
		}
		if strings.Contains(extra, w) {
			score += 3
		}
	}
	if matches := fuzzy.Find(strings.Join(q.Words, " "), []string{title}); len(matches) > 0 {
		score += matches[0].Score / 10
	}
	return score
}

func containsWord(hay, word string) bool {
	for _, tok := range strings.FieldsFunc(hay, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if tok == word {
			return true
		}
	}
	return false
}

// similarity scores how alike two titles are, from 0 to 1, using word
// overlap and character trigrams so that reworded duplicates still match.
func Similarity(a, b string) float64 {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	wa, wb := wordSet(a), wordSet(b)
	words := jaccard(wa, wb)
	tri := jaccard(trigrams(a), trigrams(b))
	return max(words, tri)
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) > 2 && !stopWords[w] {
			out[w] = true
		}
	}
	return out
}

var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true, "into": true}

func trigrams(s string) map[string]bool {
	out := map[string]bool{}
	r := []rune(" " + s + " ")
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// similarMatch is a task that resembles another.
type SimilarMatch struct {
	Task  Task
	Score float64
}

// similarThreshold is the minimum similarity to report.
const SimilarThreshold = 0.45

// similarTasks finds tasks whose title resembles the given one.
func SimilarTasks(b *Board, title string, exclude int, limit int) []SimilarMatch {
	var out []SimilarMatch
	for _, t := range b.Tasks {
		if t.ID == exclude {
			continue
		}
		if s := Similarity(title, t.Title); s >= SimilarThreshold {
			out = append(out, SimilarMatch{Task: t, Score: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Task.ID < out[j].Task.ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
