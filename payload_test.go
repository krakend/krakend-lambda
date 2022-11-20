package lambda

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/luraproject/lura/v2/config"
	"github.com/luraproject/lura/v2/logging"
	"github.com/luraproject/lura/v2/proxy"
)

func TestPayloadExtractorFactory(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := func(t *testing.T, expectedPayload string) proxy.BackendFactory {
		return BackendFactoryWithInvoker(
			logging.NoOp,
			explosiveBF,
			func(_ *Options) Invoker {
				return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
					if string(in.Payload) != expectedPayload {
						t.Errorf("invalid payload in Lambda: expected = %s, got = %s", expectedPayload, in.Payload)
					}
					return &lambda.InvokeOutput{
						Payload:    []byte(`{"message":"success"}`),
						StatusCode: aws.Int64(200),
					}, nil
				})
			},
		)
	}

	for _, tc := range []struct {
		Name            string
		Method          string
		Params          map[string]string
		Body            io.ReadCloser
		ExpectedPayload string
		ExtraConfig     map[string]interface{}
	}{
		{
			Name:            "get",
			Method:          "GET",
			Params:          map[string]string{"first_name": "fooo", "last_name": "bar"},
			ExpectedPayload: "{\"first_name\":\"fooo\",\"last_name\":\"bar\"}\n",
		},
		{
			Name:            "post",
			Method:          "POST",
			Params:          map[string]string{"function": "python37"},
			Body:            io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedPayload: `{"first_name":"foobar","last_name":"some"}`,
		},
		{
			Name:            "AWS API Gateway format empty config",
			Params:          map[string]string{"function": "python37"},
			Body:            io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedPayload: `{"first_name":"foobar","last_name":"some"}`,
			ExtraConfig: map[string]interface{}{
				"aws_api_gateway_format": map[string]interface{}{},
			},
		},
		{
			Name:            "AWS API Gateway format disabled",
			Params:          map[string]string{"function": "python37"},
			Body:            io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedPayload: `{"first_name":"foobar","last_name":"some"}`,
			ExtraConfig: map[string]interface{}{
				"aws_api_gateway_format": map[string]interface{}{
					"enabled": false,
				},
			},
		},
		{
			Name:            "AWS API Gateway v1 format enabled",
			Params:          map[string]string{"function": "python37"},
			Body:            io.NopCloser(bytes.NewBufferString(`{"first_name":"foobar","last_name":"some"}`)),
			ExpectedPayload: `{"version":"1.0","path":"","httpMethod":"","headers":{},"multiValueHeaders":null,"queryStringParameters":{},"multiValueQueryStringParameters":null,"pathParameters":{"function":"python37"},"requestContext":{"path":"","protocol":"","httpMethod":"","identity":{"userAgent":""}},"body":"{\"first_name\":\"foobar\",\"last_name\":\"some\"}"}`,
			ExtraConfig: map[string]interface{}{
				"aws_api_gateway_format": map[string]interface{}{
					"enabled": true,
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := &proxy.Request{
				Params: tc.Params,
				Body:   tc.Body,
				URL:    &url.URL{},
			}
			extra := map[string]interface{}{}
			remote := &config.Backend{
				Method: tc.Method,
				ExtraConfig: config.ExtraConfig{
					Namespace: extra,
				},
			}
			extra["function_name"] = "function_name"
			remote.ExtraConfig[Namespace] = tc.ExtraConfig
			resp, err := bf(t, tc.ExpectedPayload)(remote)(context.Background(), r)
			if err != nil {
				t.Errorf("error: %s", err)
			}

			if m, ok := resp.Data["message"]; !ok || m != "success" {
				t.Errorf("unexpected response: %v", resp.Data)
			}
		})
	}
}

func TestFromAwsApiGatewayV1Format(t *testing.T) {
	for _, tc := range []struct {
		Name     string
		Method   string
		URL      *url.URL
		Body     io.ReadCloser
		Params   map[string]string
		Headers  map[string][]string
		Query    map[string][]string
		Expected string
	}{
		{
			Name:     "simple request",
			Method:   "GET",
			URL:      &url.URL{},
			Body:     io.NopCloser(bytes.NewBufferString(`{}`)),
			Expected: `{"version":"1.0","path":"","httpMethod":"GET","headers":{},"multiValueHeaders":null,"queryStringParameters":{},"multiValueQueryStringParameters":null,"pathParameters":null,"requestContext":{"path":"","protocol":"","httpMethod":"GET","identity":{"userAgent":""}},"body":"{}"}`,
		},
		{
			Name: "headers",
			URL:  &url.URL{},
			Headers: map[string][]string{
				"header-1": {"header-1-value-1", "header-1-value-2"},
				"header-2": {"header-2-value-1", "header-2-value-2"},
			},
			Body:     io.NopCloser(bytes.NewBufferString(``)),
			Expected: `{"version":"1.0","path":"","httpMethod":"","headers":{"header-1":"header-1-value-1","header-2":"header-2-value-1"},"multiValueHeaders":{"header-1":["header-1-value-1","header-1-value-2"],"header-2":["header-2-value-1","header-2-value-2"]},"queryStringParameters":{},"multiValueQueryStringParameters":null,"pathParameters":null,"requestContext":{"path":"","protocol":"","httpMethod":"","identity":{"userAgent":""}},"body":""}`,
		},
		{
			Name: "query",
			URL:  &url.URL{},
			Query: map[string][]string{
				"query-1": {"query-1-value-1", "query-1-value-2"},
				"query-2": {"query-2-value-1", "query-2-value-2"},
			},
			Body:     io.NopCloser(bytes.NewBufferString(``)),
			Expected: `{"version":"1.0","path":"","httpMethod":"","headers":{},"multiValueHeaders":null,"queryStringParameters":{"query-1":"query-1-value-1","query-2":"query-2-value-1"},"multiValueQueryStringParameters":{"query-1":["query-1-value-1","query-1-value-2"],"query-2":["query-2-value-1","query-2-value-2"]},"pathParameters":null,"requestContext":{"path":"","protocol":"","httpMethod":"","identity":{"userAgent":""}},"body":""}`,
		},
		{
			Name: "params",
			URL:  &url.URL{},
			Params: map[string]string{
				"param1": "param1Value",
			},
			Body:     io.NopCloser(bytes.NewBufferString(``)),
			Expected: `{"version":"1.0","path":"","httpMethod":"","headers":{},"multiValueHeaders":null,"queryStringParameters":{},"multiValueQueryStringParameters":null,"pathParameters":{"param1":"param1Value"},"requestContext":{"path":"","protocol":"","httpMethod":"","identity":{"userAgent":""}},"body":""}`,
		},
		{
			Name: "user-agent",
			URL:  &url.URL{},
			Headers: map[string][]string{
				"User-Agent": {"user-agent-1", "user-agent-2"},
			},
			Body:     io.NopCloser(bytes.NewBufferString(``)),
			Expected: `{"version":"1.0","path":"","httpMethod":"","headers":{"User-Agent":"user-agent-1"},"multiValueHeaders":{"User-Agent":["user-agent-1","user-agent-2"]},"queryStringParameters":{},"multiValueQueryStringParameters":null,"pathParameters":null,"requestContext":{"path":"","protocol":"","httpMethod":"","identity":{"userAgent":"user-agent-1"}},"body":""}`,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			r := &proxy.Request{
				Params:  tc.Params,
				Body:    tc.Body,
				URL:     tc.URL,
				Headers: tc.Headers,
				Query:   tc.Query,
				Method:  tc.Method,
			}
			payload, err := fromAwsApiGatewayV1Format(r)
			if err != nil {
				t.Errorf("error: %s", err)
			}
			if string(payload) != tc.Expected {
				t.Errorf("invalid payload: expected = %s, got = %s", tc.Expected, payload)
			}
		})
	}
}
