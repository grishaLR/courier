package classifier

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grishalr/courier-social/internal/jetstream"
)

type NotificationType string

const (
	Like         NotificationType = "like"
	Favorite     NotificationType = "favorite"
	Reply        NotificationType = "reply"
	Repost       NotificationType = "repost"
	Follow       NotificationType = "follow"
	Mention      NotificationType = "mention"
	Quote        NotificationType = "quote"
	Star         NotificationType = "star"
	Issue        NotificationType = "issue"
	PullRequest  NotificationType = "pullRequest"
	RSVP         NotificationType = "rsvp"
	Subscription NotificationType = "subscription"
	Reaction     NotificationType = "reaction"
	Play         NotificationType = "play"
	Recommend    NotificationType = "recommend"
	Vote         NotificationType = "vote"
	BlogPost     NotificationType = "blogPost"
	Generic      NotificationType = "generic"
	Unknown      NotificationType = "unknown"
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
	default:
		// Unknown collection: scan for any at:// URI referencing the watched DID
		if refersTo(c.Record, watchedDID) {
			base.Type = inferTypeFromCollection(c.Collection)
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

// inferTypeFromCollection guesses the notification type from the collection name.
func inferTypeFromCollection(collection string) NotificationType {
	parts := strings.ToLower(collection)

	// App-specific matches first (more specific wins)
	switch {
	case strings.Contains(parts, "feed.star") || strings.HasSuffix(parts, ".star"):
		return Star
	case strings.Contains(parts, "feed.reaction") || strings.HasSuffix(parts, ".reaction"):
		return Reaction
	case strings.Contains(parts, "repo.issue.comment") || strings.Contains(parts, "repo.pull.comment"):
		return Reply
	case strings.Contains(parts, "repo.issue"):
		return Issue
	case strings.Contains(parts, "repo.pull"):
		return PullRequest
	case strings.Contains(parts, "calendar.rsvp"):
		return RSVP
	case strings.Contains(parts, "graph.subscription"):
		return Subscription
	case strings.Contains(parts, "grain.favorite"):
		return Favorite
	case strings.Contains(parts, "grain.comment"):
		return Reply
	case strings.Contains(parts, "feed.play") || strings.HasSuffix(parts, ".play"):
		return Play
	case strings.Contains(parts, "recommend"):
		return Recommend
	case strings.Contains(parts, "poll.vote") || strings.HasSuffix(parts, ".vote"):
		return Vote
	case strings.Contains(parts, "leaflet.graph.subscription"):
		return Subscription
	// Generic pattern matches
	case strings.Contains(parts, "favorite"):
		return Favorite
	case strings.Contains(parts, "like"):
		return Like
	case strings.Contains(parts, "follow"):
		return Follow
	case strings.Contains(parts, "reply"), strings.Contains(parts, "comment"):
		return Reply
	case strings.Contains(parts, "repost"), strings.Contains(parts, "retweet"), strings.Contains(parts, "boost"):
		return Repost
	case strings.Contains(parts, "mention"):
		return Mention
	case strings.Contains(parts, "quote"):
		return Quote
	default:
		return Generic
	}
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
