package events

import (
	"github.com/nyaruka/goflow/flows"
)

func init() {
	registerType(TypeEmailSent, func() flows.Event { return &EmailSentEvent{} })
}

// TypeEmailSent is our type for the email event
const TypeEmailSent string = "email_sent"

// EmailSentEvent events are created when an action has sent an email.
//
//   {
//     "type": "email_sent",
//     "created_on": "2006-01-02T15:04:05Z",
//     "addresses": ["foo@bar.com"],
//     "subject": "Your activation token",
//     "body": "Your activation token is AAFFKKEE"
//   }
//
// @event email_sent
type EmailSentEvent struct {
	baseEvent

	Addresses []string `json:"addresses" validate:"required,min=1"`
	Subject   string   `json:"subject" validate:"required"`
	Body      string   `json:"body"`
}

// NewEmailSent returns a new email event with the passed in subject, body and emails
func NewEmailSent(addresses []string, subject string, body string) *EmailSentEvent {
	return &EmailSentEvent{
		baseEvent: newBaseEvent(TypeEmailSent),
		Addresses: addresses,
		Subject:   subject,
		Body:      body,
	}
}
