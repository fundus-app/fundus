package triage

import "encoding/json"

// Result is the structured output the triage model must produce.
type Result struct {
	Classification string      `json:"classification"`
	Confidence     float64     `json:"confidence"`
	Summary        string      `json:"summary"`
	Question       string      `json:"question,omitempty"`
	Operations     []Operation `json:"operations"`
}

// Operation is one proposed change in the model's vocabulary. The runtime maps
// it onto core ops after validation; the model never addresses the core directly.
type Operation struct {
	Op            string   `json:"op"`
	Kind          string   `json:"kind,omitempty"`
	Title         string   `json:"title,omitempty"`
	Markdown      string   `json:"markdown,omitempty"`
	NoteID        string   `json:"note_id,omitempty"`
	Text          string   `json:"text,omitempty"`
	TaskID        string   `json:"task_id,omitempty"`
	State         string   `json:"state,omitempty"`
	Due           *string  `json:"due,omitempty"`
	EffortMinutes *int     `json:"effort_minutes,omitempty"`
	Importance    *int     `json:"importance,omitempty"`
	WaitingOn     string   `json:"waiting_on,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	Name          string   `json:"name,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
}

// Classifications the model may return.
var Classifications = []string{"note", "idea", "task", "question", "info", "correction", "research", "unclear", "discard"}

// SchemaName identifies the triage schema to providers.
const SchemaName = "triage"

// Schema is the JSON schema for Result.
var Schema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["classification", "confidence", "summary", "question", "operations"],
  "properties": {
    "classification": {"type": "string", "enum": ["note", "idea", "task", "question", "info", "correction", "research", "unclear", "discard"]},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1},
    "summary": {"type": "string", "description": "One short sentence, in the language of the capture text, saying what was filed."},
    "question": {"type": ["string", "null"], "description": "Only when classification is unclear: the one question whose answer decides how to file this."},
    "operations": {
      "type": "array",
      "maxItems": 12,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["op"],
        "properties": {
          "op": {"type": "string", "enum": ["note.create", "note.append", "task.create", "task.complete", "task.mention", "task.update", "topic.create"]},
          "kind": {"type": "string", "description": "note.create: note|idea. topic.create: topic|person|project."},
          "title": {"type": "string"},
          "markdown": {"type": "string", "description": "Body for note.create / note.append. Headings, paragraphs, lists, quotes only."},
          "note_id": {"type": "string", "description": "note.append: id of an existing note from the context."},
          "text": {"type": "string", "description": "task.create/task.update: the task in imperative form."},
          "task_id": {"type": "string", "description": "task.complete/task.mention/task.update: id of an existing task from the context."},
          "state": {"type": "string", "description": "task.create/task.update: open|later|waiting|done."},
          "due": {"type": ["string", "null"], "description": "YYYY-MM-DD, only when the user states a date or deadline."},
          "effort_minutes": {"type": ["integer", "null"], "description": "Only when the user gives an estimate."},
          "importance": {"type": ["integer", "null"], "description": "1 low, 2 normal, 3 high; only when the user signals it."},
          "waiting_on": {"type": "string"},
          "topics": {"type": "array", "items": {"type": "string"}, "description": "Existing topic ids from the context, or new topic names."},
          "name": {"type": "string", "description": "topic.create: the topic name."},
          "aliases": {"type": "array", "items": {"type": "string"}}
        }
      }
    }
  }
}`)
