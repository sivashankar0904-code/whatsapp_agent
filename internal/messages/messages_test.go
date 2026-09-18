package messages

import (
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestExtractText(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{
			name: "plain conversation",
			msg:  &waE2E.Message{Conversation: proto.String("hello")},
			want: "hello",
		},
		{
			name: "extended text (link or reply)",
			msg: &waE2E.Message{
				ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("see https://x")},
			},
			want: "see https://x",
		},
		{
			name: "image caption",
			msg: &waE2E.Message{
				ImageMessage: &waE2E.ImageMessage{Caption: proto.String("nice view")},
			},
			want: "nice view",
		},
		{
			name: "wrapped in DeviceSentMessage (fromMe via another linked device)",
			msg: &waE2E.Message{
				DeviceSentMessage: &waE2E.DeviceSentMessage{
					Message: &waE2E.Message{Conversation: proto.String("sent from phone")},
				},
			},
			want: "sent from phone",
		},
		{
			name: "sender key distribution (group key exchange, not user content)",
			msg: &waE2E.Message{
				SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{},
			},
			want: "",
		},
		{
			name: "nil message",
			msg:  nil,
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractText(c.msg); got != c.want {
				t.Errorf("ExtractText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExtractYouTubeLinks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "no link",
			text: "just chatting, nothing here",
			want: nil,
		},
		{
			name: "watch url with scheme",
			text: "check this out https://www.youtube.com/watch?v=dQw4w9WgXcQ nice",
			want: []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		},
		{
			name: "youtu.be short link, no scheme prefix text around it",
			text: "http://youtu.be/dQw4w9WgXcQ",
			want: []string{"http://youtu.be/dQw4w9WgXcQ"},
		},
		{
			name: "shorts link",
			text: "https://youtube.com/shorts/abc123XYZ_-",
			want: []string{"https://youtube.com/shorts/abc123XYZ_-"},
		},
		{
			name: "multiple links in one message",
			text: "one: https://youtu.be/aaaaaaaaaaa two: https://www.youtube.com/watch?v=bbbbbbbbbbb",
			want: []string{"https://youtu.be/aaaaaaaaaaa", "https://www.youtube.com/watch?v=bbbbbbbbbbb"},
		},
		{
			name: "non-youtube link is ignored",
			text: "see https://example.com/watch?v=dQw4w9WgXcQ",
			want: nil,
		},
		{
			name: "empty text",
			text: "",
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractYouTubeLinks(c.text)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ExtractYouTubeLinks(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}
