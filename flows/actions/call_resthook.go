package actions

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"

	"github.com/pkg/errors"
)

// ResthookPayload is the POST payload used by resthooks
const ResthookPayload = `@(json(object(
  "contact", object("uuid", contact.uuid, "name", contact.name, "urn", contact.urn),
  "flow", run.flow,
  "path", run.path,
  "results", foreach_value(results, extract_object, "category", "category_localized", "created_on", "input", "name", "node_uuid", "value"),
  "run", object("uuid", run.uuid, "created_on", run.created_on),
  "input", if(
    input,
    object(
      "attachments", foreach(input.attachments, attachment_parts),
      "channel", input.channel,
      "created_on", input.created_on,
      "text", input.text,
      "type", input.type,
      "urn", if(
        input.urn,
        object(
          "display", default(format_urn(input.urn), ""),
          "path", urn_parts(input.urn).path,
          "scheme", urn_parts(input.urn).scheme
        ),
        null
      ),
      "uuid", input.uuid
    ),
    null
  ),
  "channel", default(input.channel, null)
)))`

func init() {
	registerType(TypeCallResthook, func() flows.Action { return &CallResthookAction{} })
}

// TypeCallResthook is the type for the call resthook action
const TypeCallResthook string = "call_resthook"

// CallResthookAction can be used to call a resthook.
//
// A [event:webhook_called] event will be created for each subscriber of the resthook with the results
// of the HTTP call. If the action has `result_name` set, a result will
// be created with that name, and if the resthook returns valid JSON, that will be accessible
// through `extra` on the result.
//
//   {
//     "uuid": "8eebd020-1af5-431c-b943-aa670fc74da9",
//     "type": "call_resthook",
//     "resthook": "new-registration"
//   }
//
// @action call_resthook
type CallResthookAction struct {
	baseAction
	onlineAction

	Resthook   string `json:"resthook" validate:"required"`
	ResultName string `json:"result_name,omitempty"`
}

// NewCallResthook creates a new call resthook action
func NewCallResthook(uuid flows.ActionUUID, resthook string, resultName string) *CallResthookAction {
	return &CallResthookAction{
		baseAction: newBaseAction(TypeCallResthook, uuid),
		Resthook:   resthook,
		ResultName: resultName,
	}
}

// Execute runs this action
func (a *CallResthookAction) Execute(run flows.FlowRun, step flows.Step, logModifier flows.ModifierCallback, logEvent flows.EventCallback) error {
	// NOOP if resthook doesn't exist
	resthook := run.Session().Assets().Resthooks().FindBySlug(a.Resthook)
	if resthook == nil {
		return nil
	}

	// build our payload
	payload, err := run.EvaluateTemplate(ResthookPayload)
	if err != nil {
		// if we got an error then our payload is likely not valid JSON
		return errors.Wrapf(err, "error evaluating resthook payload")
	}

	// check the payload is valid JSON - it ends up in the session so needs to be valid
	if !json.Valid([]byte(payload)) {
		return errors.Errorf("resthook payload evaluation produced invalid JSON: %s", payload)
	}

	// regardless of what subscriber calls we make, we need to record the payload that would be sent
	logEvent(events.NewResthookCalled(a.Resthook, json.RawMessage(payload)))

	// make a call to each subscriber URL
	calls := make([]*flows.WebhookCall, 0, len(resthook.Subscribers()))

	for _, url := range resthook.Subscribers() {
		req, err := http.NewRequest("POST", url, strings.NewReader(payload))
		if err != nil {
			logEvent(events.NewError(err))
			return nil
		}

		req.Header.Add("Content-Type", "application/json")

		svc, err := run.Session().Engine().Services().Webhook(run.Session())
		if err != nil {
			logEvent(events.NewError(err))
			return nil
		}

		call, err := svc.Call(run.Session(), req)

		if err != nil {
			logEvent(events.NewError(err))
		}
		if call != nil {
			calls = append(calls, call)
			logEvent(events.NewWebhookCalled(call, callStatus(call, true), a.Resthook))
		}
	}

	asResult := a.pickResultCall(calls)
	if asResult != nil {
		run.SetWebhook(types.JSONToXValue(utils.ExtractResponseJSON([]byte(asResult.Response))))
	}

	if a.ResultName != "" {
		if asResult != nil {
			a.saveWebhookResult(run, step, a.ResultName, asResult, callStatus(asResult, true), false, logEvent)
		} else {
			a.saveResult(run, step, a.ResultName, "no subscribers", "Failure", "", "", nil, logEvent)
		}
	}

	return nil
}

// picks one of the resthook calls to become the result generated by this action
func (a *CallResthookAction) pickResultCall(calls []*flows.WebhookCall) *flows.WebhookCall {
	var lastSuccess, last410, lastFailure *flows.WebhookCall

	for _, call := range calls {
		if call.StatusCode/100 == 2 {
			lastSuccess = call
		} else if call.StatusCode == 410 {
			last410 = call
		} else {
			lastFailure = call
		}
	}

	// 1. if we got one or more errors (non-410, non-200), result is last failure
	// 2. if we no errors, no 410s, but at least one success, result is last success
	// 3. if we only got 410s, result is last 410
	if lastFailure != nil {
		return lastFailure
	} else if lastSuccess != nil {
		return lastSuccess
	}
	return last410
}

// Results enumerates any results generated by this flow object
func (a *CallResthookAction) Results(node flows.Node, include func(*flows.ResultInfo)) {
	if a.ResultName != "" {
		include(flows.NewResultInfo(a.ResultName, webhookCategories, node))
	}
}
