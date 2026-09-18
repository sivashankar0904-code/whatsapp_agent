package main

import (
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
			if got := extractText(c.msg); got != c.want {
				t.Errorf("extractText() = %q, want %q", got, c.want)
			}
		})
	}
}
