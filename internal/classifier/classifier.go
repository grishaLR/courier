package classifier

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grishalr/courier-social/internal/jetstream"
)

type NotificationType string

const (
	Like    NotificationType = "like"
	Reply   NotificationType = "reply"
	Repost  NotificationType = "repost"
	Follow  NotificationType = "follow"
	Mention NotificationType = "mention"
	Quote   NotificationType = "quote"
	Generic NotificationType = "generic"
	Unknown NotificationType = "unknown"
)

type Notification struct {
	Type       NotificationType `json:"type"`
	FromDID    string           `json:"fromDid"`
	ForDID     string           `json:"forDid"`
	Collection string           `json:"collection"`
	URI        string           `json:"uri"`
	SubjectURI string           `json:"subjectUri,omitempty"`
	Record     json.RawMessage  `json:"record,omitempty"`
}

// Classify takes a Jetstream event and the DID of the user being watched.
// Returns nil if the event should be skipped (e.g., own writes).
func Classify(event *jetstream.Event, watchedDID string) *Notification {
	// Skip own writes
	if event.Did == watchedDID {
		return nil
	}

	if event.Kind != "commit" || event.Commit == nil {
		return nil
	}

	// Only process creates
	if event.Commit.Operation != "create" {
		return nil
	}

	c := event.Commit
	uri := fmt.Sprintf("at://%s/%s/%s", event.Did, c.Collection, c.RKey)

	base := &Notification{
		FromDID:    event.Did,
		ForDID:     watchedDID,
		Collection: c.Collection,
		URI:        uri,
		Record:     c.Record,
	}

	switch c.Collection {
	case "app.bsky.feed.like":
		if refersTo(c.Record, watchedDID) {
			base.Type = Like
			base.SubjectURI = extractSubjectURI(c.Record)
			return base
		}
	case "app.bsky.feed.post":
		return classifyPost(c.Record, watchedDID, base)
	case "app.bsky.feed.repost":
		if refersTo(c.Record, watchedDID) {
			base.Type = Repost
			base.SubjectURI = extractSubjectURI(c.Record)
			return base
		}
	case "app.bsky.graph.follow":
		if subjectIs(c.Record, watchedDID) {
			base.Type = Follow
			return base
		}
	// Tangled
	case "sh.tangled.feed.reaction":
		if refersTo(c.Record, watchedDID) {
			base.Type = Like
			base.SubjectURI = extractFirstATURI(c.Record, watchedDID)
			return base
		}
	case "sh.tangled.repo.comment":
		if refersTo(c.Record, watchedDID) {
			base.Type = Reply
			base.SubjectURI = extractFirstATURI(c.Record, watchedDID)
			return base
		}
	case "sh.tangled.graph.follow":
		if subjectIs(c.Record, watchedDID) {
			base.Type = Follow
			return base
		}
	default:
		// Unknown collection: scan for any at:// URI referencing the watched DID
		if refersTo(c.Record, watchedDID) {
			base.Type = Generic
			base.SubjectURI = extractFirstATURI(c.Record, watchedDID)
			return base
		}
	}

	return nil
}

func classifyPost(record json.RawMessage, watchedDID string, base *Notification) *Notification {
	var post struct {
		Reply *struct {
			Parent struct {
				URI string `json:"uri"`
			} `json:"parent"`
		} `json:"reply,omitempty"`
		Facets []struct {
			Features []struct {
				Type string `json:"$type"`
				DID  string `json:"did"`
			} `json:"features"`
		} `json:"facets,omitempty"`
		Embed *struct {
			Type   string `json:"$type"`
			Record *struct {
				URI string `json:"uri"`
			} `json:"record,omitempty"`
		} `json:"embed,omitempty"`
	}

	if err := json.Unmarshal(record, &post); err != nil {
		return nil
	}

	// Reply to watched user's post
	if post.Reply != nil && strings.Contains(post.Reply.Parent.URI, watchedDID) {
		base.Type = Reply
		base.SubjectURI = post.Reply.Parent.URI
		return base
	}

	// Quote post
	if post.Embed != nil && post.Embed.Record != nil && strings.Contains(post.Embed.Record.URI, watchedDID) {
		base.Type = Quote
		base.SubjectURI = post.Embed.Record.URI
		return base
	}

	// Mention
	for _, facet := range post.Facets {
		for _, feature := range facet.Features {
			if feature.Type == "app.bsky.richtext.facet#mention" && feature.DID == watchedDID {
				base.Type = Mention
				return base
			}
		}
	}

	return nil
}

// refersTo scans raw JSON for any at:// URI containing the given DID.
func refersTo(record json.RawMessage, did string) bool {
	return strings.Contains(string(record), "at://"+did)
}

// subjectIs checks if a record's "subject" field equals the given DID.
func subjectIs(record json.RawMessage, did string) bool {
	var r struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(record, &r); err != nil {
		return false
	}
	return r.Subject == did
}

// extractSubjectURI pulls the subject.uri from records like likes and reposts.
func extractSubjectURI(record json.RawMessage) string {
	var r struct {
		Subject struct {
			URI string `json:"uri"`
		} `json:"subject"`
	}
	if err := json.Unmarshal(record, &r); err != nil {
		return ""
	}
	return r.Subject.URI
}

// extractFirstATURI finds the first at:// URI in the record JSON that references the given DID.
func extractFirstATURI(record json.RawMessage, did string) string {
	s := string(record)
	target := "at://" + did
	idx := strings.Index(s, target)
	if idx < 0 {
		return ""
	}
	// Extract the full URI — scan forward until we hit a quote, space, or end
	end := idx
	for end < len(s) && s[end] != '"' && s[end] != ' ' && s[end] != ',' && s[end] != '}' {
		end++
	}
	return s[idx:end]
}
