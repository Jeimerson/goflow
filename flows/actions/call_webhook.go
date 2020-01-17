package actions

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/nyaruka/goflow/excellent/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"

	"github.com/pkg/errors"
	"golang.org/x/net/http/httpguts"
)

func isValidURL(u string) bool { _, err := url.Parse(u); return err == nil }

func init() {
	registerType(TypeCallWebhook, func() flows.Action { return &CallWebhookAction{} })
}

// TypeCallWebhook is the type for the call webhook action
const TypeCallWebhook string = "call_webhook"

// CallWebhookAction can be used to call an external service. The body, header and url fields may be
// templates and will be evaluated at runtime. A [event:webhook_called] event will be created based on
// the results of the HTTP call. If this action has a `result_name`, then addtionally it will create
// a new result with that name. If the webhook returned valid JSON, that will be accessible
// through `extra` on the result.
//
//   {
//     "uuid": "8eebd020-1af5-431c-b943-aa670fc74da9",
//     "type": "call_webhook",
//     "method": "GET",
//     "url": "http://localhost:49998/?cmd=success",
//     "headers": {
//       "Authorization": "Token AAFFZZHH"
//     },
//     "result_name": "webhook"
//   }
//
// @action call_webhook
type CallWebhookAction struct {
	baseAction
	onlineAction

	Method          string            `json:"method" validate:"required,http_method"`
	URL             string            `json:"url" validate:"required" engine:"evaluated"`
	Headers         map[string]string `json:"headers,omitempty" engine:"evaluated"`
	Body            string            `json:"body,omitempty" engine:"evaluated"`
	ResultName      string            `json:"result_name,omitempty"`
	ResponseAsExtra bool              `json:"response_as_extra,omitempty"`
}

// NewCallWebhook creates a new call webhook action
func NewCallWebhook(uuid flows.ActionUUID, method string, url string, headers map[string]string, body string, resultName string, responseAsExtra bool) *CallWebhookAction {
	return &CallWebhookAction{
		baseAction:      newBaseAction(TypeCallWebhook, uuid),
		Method:          method,
		URL:             url,
		Headers:         headers,
		Body:            body,
		ResultName:      resultName,
		ResponseAsExtra: responseAsExtra,
	}
}

// Validate validates our action is valid
func (a *CallWebhookAction) Validate() error {
	for key := range a.Headers {
		if !httpguts.ValidHeaderFieldName(key) {
			return errors.Errorf("header '%s' is not a valid HTTP header", key)
		}
	}

	return nil
}

// Execute runs this action
func (a *CallWebhookAction) Execute(run flows.FlowRun, step flows.Step, logModifier flows.ModifierCallback, logEvent flows.EventCallback) error {

	// substitute any variables in our url
	url, err := run.EvaluateTemplate(a.URL)
	if err != nil {
		logEvent(events.NewError(err))
	}
	if url == "" {
		logEvent(events.NewErrorf("webhook URL evaluated to empty string"))
		return nil
	}
	if !isValidURL(url) {
		logEvent(events.NewErrorf("webhook URL evaluated to an invalid URL: '%s'", url))
		return nil
	}

	method := strings.ToUpper(a.Method)
	body := a.Body

	// substitute any body variables
	if body != "" {
		body, err = run.EvaluateTemplate(body)
		if err != nil {
			logEvent(events.NewError(err))
		}
	}

	return a.call(run, step, url, method, body, logEvent)
}

// Execute runs this action
func (a *CallWebhookAction) call(run flows.FlowRun, step flows.Step, url, method, body string, logEvent flows.EventCallback) error {
	// build our request
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return err
	}

	// add the custom headers, substituting any template vars
	for key, value := range a.Headers {
		headerValue, err := run.EvaluateTemplate(value)
		if err != nil {
			logEvent(events.NewError(err))
		}

		req.Header.Add(key, headerValue)
	}

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
		status := callStatus(call, false)

		logEvent(events.NewWebhookCalled(call, status, ""))

		run.SetWebhook(types.JSONToXValue(utils.ExtractResponseJSON([]byte(call.Response))))

		if a.ResultName != "" {
			a.saveWebhookResult(run, step, a.ResultName, call, status, a.ResponseAsExtra, logEvent)
		}
	}

	return nil
}

// Results enumerates any results generated by this flow object
func (a *CallWebhookAction) Results(node flows.Node, include func(*flows.ResultInfo)) {
	if a.ResultName != "" {
		include(flows.NewResultInfo(a.ResultName, webhookCategories, node))
	}
}

// determines the webhook status from the HTTP status code
func callStatus(call *flows.WebhookCall, isResthook bool) flows.CallStatus {
	if call.StatusCode == 0 {
		return flows.CallStatusConnectionError
	}
	if isResthook && call.StatusCode == 410 {
		// https://zapier.com/developer/documentation/v2/rest-hooks/
		return flows.CallStatusSubscriberGone
	}
	if call.StatusCode/100 == 2 {
		return flows.CallStatusSuccess
	}
	return flows.CallStatusResponseError
}
