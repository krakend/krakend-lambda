//go:build integration

package test

import (
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"testing"
	"time"

	krakendlambda "github.com/krakendio/krakend-lambda/v2"
	"github.com/luraproject/lura/v2/config"
	"github.com/luraproject/lura/v2/logging"
	"github.com/luraproject/lura/v2/proxy"
)

var endpoint = flag.String("aws_endpoint", "http://192.168.99.100:4574", "url of the localstack's endpoint")

func TestLocalStack(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	logger, _ := logging.NewLogger("ERROR", os.Stdout, "")

	bf := krakendlambda.BackendFactory(logger, explosiveBF)

	for i, tc := range []struct {
		Name        string
		Method      string
		Key         string
		Function    string
		ExpectedMsg string
		Params      map[string]string
		Body        io.ReadCloser
	}{
		{
			Name:        "get_with_default_key",
			Method:      "GET",
			Params:      map[string]string{"function": "python37", "first_name": "fooo", "last_name": "bar"},
			ExpectedMsg: "Hello fooo bar!",
		},
		{
			Name:        "post_with_default_key",
			Method:      "POST",
			Params:      map[string]string{"function": "python37"},
			Body:        io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedMsg: "Hello foobar some!",
		},
		{
			Name:        "get_with_custom_key",
			Method:      "GET",
			Params:      map[string]string{"function": "unknown", "lambda": "python37", "first_name": "fooo", "last_name": "bar"},
			Key:         "lambda",
			ExpectedMsg: "Hello fooo bar!",
		},
		{
			Name:        "post_with_custom_key",
			Method:      "POST",
			Params:      map[string]string{"function": "unknown", "lambda": "python37"},
			Body:        io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			Key:         "lambda",
			ExpectedMsg: "Hello foobar some!",
		},
		{
			Name:        "get_with_function_name",
			Method:      "GET",
			Params:      map[string]string{"function": "unknown", "first_name": "fooo", "last_name": "bar"},
			ExpectedMsg: "Hello fooo bar!",
			Function:    "python37",
		},
		{
			Name:        "post_with_function_name",
			Method:      "POST",
			Params:      map[string]string{"function": "unknown"},
			Body:        io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedMsg: "Hello foobar some!",
			Function:    "python37",
		},
		{
			Name:        "get_with_function_name_and_key",
			Method:      "GET",
			Params:      map[string]string{"function": "unknown", "lambda": "unknown", "first_name": "fooo", "last_name": "bar"},
			Key:         "lambda",
			ExpectedMsg: "Hello fooo bar!",
			Function:    "python37",
		},
		{
			Name:        "post_with_function_name_and_key",
			Method:      "POST",
			Params:      map[string]string{"function": "unknown", "lambda": "unknown"},
			Body:        io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			Key:         "lambda",
			ExpectedMsg: "Hello foobar some!",
			Function:    "python37",
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := &proxy.Request{
				Params: tc.Params,
				Body:   tc.Body,
			}
			extra := map[string]interface{}{
				"region":   "us-east-1",
				"endpoint": *endpoint,
			}
			remote := &config.Backend{
				Method: tc.Method,
				ExtraConfig: config.ExtraConfig{
					krakendlambda.Namespace: extra,
				},
			}
			if tc.Key != "" {
				extra["function_param_name"] = tc.Key
			}
			if tc.Function != "" {
				extra["function_name"] = tc.Function
			}
			remote.ExtraConfig[krakendlambda.Namespace] = extra

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			resp, err := bf(remote)(ctx, r)
			if err != nil {
				t.Error(i, err)
				return
			}
			if !resp.IsComplete {
				t.Errorf("%d: incomplete response", i)
				return
			}
			if m, ok := resp.Data["message"]; !ok || m != tc.ExpectedMsg {
				t.Errorf("unexpected response: %v", resp.Data)
			}
		})
	}
}
