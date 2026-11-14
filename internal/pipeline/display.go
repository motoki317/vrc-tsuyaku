package pipeline

import (
	"context"
	"time"

	"github.com/motoki317/vrc-tsuyaku/internal/chatbox"
)

// display groups a flow's phrases into messages and times their pages.
// A message is one or more phrases shown together: one chatbox text, and one bubble in the web UI.
// A phrase joins the latest message while that message has one page and is in its reading time,
// if both have the same template and languages and fit in one page together.
// With send, a page that replaces another waits for that page's reading time; a joined page does not.
// Without send the flow has no chatbox. The web UI keeps every message, so nothing waits.
type display struct {
	send      func(context.Context, string) error
	emit      func(Event)
	format    string
	message   *chatbox.Message
	firstID   uint64
	since     time.Time
	readUntil time.Time
}

func (d *display) show(ctx context.Context, id uint64, format string, message chatbox.Message, queued time.Time) error {
	shown := time.Now()
	pages, err := chatbox.Render(format, message)
	if err != nil {
		if d.send == nil {
			// The web UI still shows the phrase as its own bubble, so a later phrase must not join the message before it.
			d.message = nil
			return nil
		}
		return err
	}
	if len(pages) == 0 {
		return nil
	}
	merged := false
	if current := d.message; current != nil && time.Now().Before(d.readUntil) &&
		d.format == format && current.SourceLang == message.SourceLang && current.TargetLang == message.TargetLang {
		joined := current.Join(message)
		if joinedPages, err := chatbox.Render(format, joined); err == nil && len(joinedPages) == 1 {
			message, pages, merged = joined, joinedPages, true
		}
	}
	for i, page := range pages {
		if d.send != nil && !merged {
			due := d.readUntil
			if limit := queued.Add(time.Duration(i+1) * chatbox.PageReadingTime); limit.Before(due) {
				due = limit
			}
			if delay := time.Until(due); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return context.Cause(ctx)
				case <-timer.C:
				}
			}
		}
		if d.send != nil {
			if err := d.send(ctx, page); err != nil {
				return err
			}
			shown = time.Now()
		}
		if !merged {
			d.since, d.firstID = shown, id
		}
		d.readUntil = chatbox.ReadUntil(page, d.since, shown)
		d.message = nil
		if len(pages) == 1 {
			d.format, d.message = format, &message
		}
		if d.send != nil {
			d.emit(Event{PhraseID: id, Kind: "page", Text: page, SourceLang: message.SourceLang, TargetLang: message.TargetLang})
		}
		if merged {
			d.emit(Event{PhraseID: id, Kind: "merged", Into: d.firstID, Original: message.Original, Text: message.Translation, SourceLang: message.SourceLang, TargetLang: message.TargetLang})
		}
	}
	return nil
}
