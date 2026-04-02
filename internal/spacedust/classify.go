package spacedust

import (
	"github.com/grishalr/courier-social/internal/classifier"
)

// ClassifyLink converts a Spacedust link event into a Notification.
func ClassifyLink(event *Event) *classifier.Notification {
	link := event.Link
	fromDID := link.SourceDID()
	collection := link.Collection()

	notif := &classifier.Notification{
		FromDID:    fromDID,
		ForDID:     link.Subject, // may be a DID or AT URI
		Collection: collection,
		URI:        link.SourceRecord,
		SubjectURI: link.Subject,
	}

	switch {
	case collection == "app.bsky.feed.like":
		notif.Type = classifier.Like
	case collection == "app.bsky.feed.repost":
		notif.Type = classifier.Repost
	case collection == "app.bsky.graph.follow":
		notif.Type = classifier.Follow
	case collection == "app.bsky.feed.post":
		// Could be reply, quote, or mention depending on the source path
		switch link.Source {
		case "app.bsky.feed.post:reply.parent.uri":
			notif.Type = classifier.Reply
		case "app.bsky.feed.post:embed.record.uri":
			notif.Type = classifier.Quote
		case "app.bsky.feed.post:facets.features.did":
			notif.Type = classifier.Mention
		default:
			notif.Type = classifier.Reply // fallback
		}
	// Tangled
	case collection == "sh.tangled.feed.reaction":
		notif.Type = classifier.Like
	case collection == "sh.tangled.repo.comment":
		notif.Type = classifier.Reply
	default:
		notif.Type = classifier.Generic
	}

	return notif
}
