package core

import (
	"fmt"
	"time"

	"github.com/fundus-app/fundus/internal/model"
)

// Score computes the attention score of a task from the evidence it carries.
// It is a reasoned ordering, never a stored fact. Every contribution is
// returned as a human-readable reason so the UI can explain the ranking.
func Score(t *model.Task, now time.Time, topicActivity map[string]time.Time) (float64, []string) {
	var score float64
	var reasons []string
	add := func(v float64, why string) {
		score += v
		reasons = append(reasons, why)
	}
	switch t.Importance {
	case 3:
		add(4, "marked important")
	case 2:
		add(2, "normal importance")
	case 1:
		add(0.5, "low importance")
	}
	if t.Due != "" {
		if due, err := time.ParseInLocation("2006-01-02", t.Due, now.Location()); err == nil {
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			days := int(due.Sub(today).Hours() / 24)
			switch {
			case days < 0:
				add(6, fmt.Sprintf("overdue by %d day%s", -days, plural(-days)))
			case days == 0:
				add(5, "due today")
			case days <= 3:
				add(3, fmt.Sprintf("due in %d day%s", days, plural(days)))
			case days <= 7:
				add(1.5, "due this week")
			default:
				add(0.5, "has a due date")
			}
		}
	}
	if t.Mentions > 0 {
		m := float64(t.Mentions)
		if m > 3 {
			m = 3
		}
		add(0.75*m, fmt.Sprintf("mentioned %d more time%s", t.Mentions, plural(t.Mentions)))
	}
	if age := now.Sub(t.CreatedAt); age < 7*24*time.Hour && age >= 0 {
		add(1, "captured recently")
	}
	if t.EffortMinutes > 0 && t.EffortMinutes <= 30 {
		add(0.5, "quick win")
	}
	for _, tid := range t.Topics {
		if last, ok := topicActivity[tid]; ok && now.Sub(last) < 14*24*time.Hour {
			add(1, "topic is active")
			break
		}
	}
	return score, reasons
}
