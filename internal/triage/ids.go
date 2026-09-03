package triage

import "github.com/fundus-app/fundus/internal/ids"

func newTopicID() string { return ids.New(ids.PrefixTopic) }
