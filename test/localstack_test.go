package test

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	krakendlambda "github.com/devopsfaith/krakend-lambda"
	"github.com/devopsfaith/krakend/config"
	"github.com/devopsfaith/krakend/proxy"
)

func TestLocalStack(t *testing.T) {
	sess := session.Must(session.NewSession(&aws.Config{
		Region:   aws.String("us-east-1"),
		Endpoint: aws.String("http://192.168.99.100:4574"),
	}))

	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := krakendlambda.BackendFactoryWithInvoker(explosiveBF, lambda.New(sess))

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
			Body:        ioutil.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
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
			Body:        ioutil.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
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
			Body:        ioutil.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
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
			Body:        ioutil.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
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
			extra := map[string]interface{}{}
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
			resp, err := bf(remote)(context.Background(), r)
			if err != nil {
				t.Error(i, err)
			}
			if !resp.IsComplete {
				t.Errorf("%d: incomplete response", i)
			}
			if m, ok := resp.Data["message"]; !ok || m != tc.ExpectedMsg {
				t.Errorf("unexpected response: %v", resp.Data)
			}
		})
	}
}
