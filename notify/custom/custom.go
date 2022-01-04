// Copyright 2022 Prometheus Team
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-kit/log"
	commoncfg "github.com/prometheus/common/config"

	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/notify"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/alertmanager/types"
)

// Notifier implements a Notifier for custom webhooks.
type Notifier struct {
	conf    *config.CustomConfig
	tmpl    *template.Template
	logger  log.Logger
	client  *http.Client
	retrier *notify.Retrier
}

// New returns a new custom Webhook.
func New(conf *config.CustomConfig, t *template.Template, l log.Logger, httpOpts ...commoncfg.HTTPClientOption) (*Notifier, error) {
	client, err := commoncfg.NewClientFromConfig(*conf.HTTPConfig, "custom", httpOpts...)
	if err != nil {
		return nil, err
	}
	return &Notifier{
		conf:   conf,
		tmpl:   t,
		logger: l,
		client: client,
		// Webhooks are assumed to respond with 2xx response codes on a successful
		// request and 5xx response codes are assumed to be recoverable.
		retrier: &notify.Retrier{
			RetryCodes: conf.RetryCodes,
		},
	}, nil
}

// Notify implements the Notifier interface.
func (n *Notifier) Notify(ctx context.Context, alerts ...*types.Alert) (bool, error) {
	var err error
	var (
		data     = notify.GetTemplateData(ctx, n.tmpl, alerts, n.logger)
		tmplText = notify.TmplText(n.tmpl, data, &err)
	)

	url := tmplText(string(n.conf.URL))
	body := customTemplate(tmplText, n.conf.Body)
	if err != nil {
		return false, err
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return false, err
	}

	// TODO: use http library directly to make it possible to set headers, etc,
	// as the config promises would be possible.
	resp, err := notify.PostJSON(ctx, n.client, url, &buf)
	if err != nil {
		return true, err
	}

	notify.Drain(resp)

	return n.retrier.Check(resp.StatusCode, nil)
}

func customTemplate(tmplText func(string) string, body map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range body {
		out[k] = customItem(tmplText, v)
	}
	return out
}

func customItem(tmplText func(string) string, v interface{}) interface{} {
	switch val := v.(type) {
	case string: // template
		return tmplText(val)
	case map[string]interface{}: // object
		return customTemplate(tmplText, val)
	case []interface{}: // array
		var out []interface{}
		for _, item := range val {
			out = append(out, customItem(tmplText, item))
		}
		return out
	case interface{}:
		// other, copy as is
		return val
	}
	// unreachable; go can't work out the type switch above matches the input type
	return "<unknown>"
}
