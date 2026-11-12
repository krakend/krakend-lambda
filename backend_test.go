package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/luraproject/lura/v3/config"
	"github.com/luraproject/lura/v3/logging"
	"github.com/luraproject/lura/v3/proxy"
)

func TestBackendFactoryWithInvoker_fallback(t *testing.T) {
	for i, tc := range []struct {
		Name string
		Cfg  *config.Backend
	}{
		{
			Name: "no extra",
			Cfg: &config.Backend{
				Method: "GET",
			},
		},
		{
			Name: "wrong extra type",
			Cfg: &config.Backend{
				Method: "GET",
				ExtraConfig: config.ExtraConfig{
					Namespace: 42,
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			hits := 0

			bf := BackendFactoryWithInvoker(
				logging.NoOp,
				func(remote *config.Backend) proxy.Proxy {
					hits++
					return proxy.NoopProxy
				},
				func(_ *Options) Invoker {
					return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
						t.Error("this invoker should not been called")
						return nil, nil
					})
				},
			)

			resp, err := bf(tc.Cfg)(context.Background(), &proxy.Request{})
			if err != nil {
				t.Error(i, err)
			}
			if resp != nil {
				t.Errorf("%d: unexpected response", i)
			}
			if hits != 1 {
				t.Errorf("unexpected number of hits to the fallback backend factory: %d", hits)
			}
		})
	}
}

func TestBackendFactoryWithInvoker(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := BackendFactoryWithInvoker(
		logging.NoOp,
		explosiveBF,
		func(_ *Options) Invoker {
			return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
				if in.InvocationType != types.InvocationTypeRequestResponse {
					t.Errorf("unexpected InvocationType: %s", in.InvocationType)
				}
				// if *in.ClientContext != clientContext {
				// 	t.Errorf("unexpected ClientContext: %s", *in.ClientContext)
				// }
				if *in.FunctionName != "python37" {
					t.Errorf("unexpected FunctionName: %s", *in.FunctionName)
				}
				payload := map[string]string{}
				if err := json.Unmarshal(in.Payload, &payload); err != nil {
					t.Error(err)
					return nil, err
				}
				if payload["first_name"] == "" {
					t.Errorf("first_name not present in the payload request")
				}
				if payload["last_name"] == "" {
					t.Errorf("last_name not present in the payload request")
				}
				return &lambda.InvokeOutput{
					Payload:    []byte(fmt.Sprintf(`{"message":"Hello %s %s!", "foo": 42}`, payload["first_name"], payload["last_name"])),
					StatusCode: 200,
				}, nil
			})
		},
	)

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
				Method:   tc.Method,
				DenyList: []string{"foo"},
				ExtraConfig: config.ExtraConfig{
					Namespace: extra,
				},
			}
			if tc.Key != "" {
				extra["function_param_name"] = tc.Key
			}
			if tc.Function != "" {
				extra["function_name"] = tc.Function
			}
			remote.ExtraConfig[Namespace] = extra
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
			if _, ok := resp.Data["foo"]; ok {
				t.Errorf("unexpected response: %v", resp.Data)
			}
		})
	}
}

func TestBackendFactoryWithInvoker_error(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := BackendFactoryWithInvoker(
		logging.NoOp,
		explosiveBF,
		func(_ *Options) Invoker {
			return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
				if in.InvocationType != types.InvocationTypeRequestResponse {
					t.Errorf("unexpected InvocationType: %s", in.InvocationType)
				}
				// if *in.ClientContext != clientContext {
				// 	t.Errorf("unexpected ClientContext: %s", *in.ClientContext)
				// }
				if *in.FunctionName != "python37" {
					t.Errorf("unexpected FunctionName: %s", *in.FunctionName)
				}
				payload := map[string]string{}
				if err := json.Unmarshal(in.Payload, &payload); err != nil {
					return nil, err
				}
				if payload["first_name"] != "" {
					t.Errorf("first_name present in the payload request")
				}
				if payload["last_name"] != "" {
					t.Errorf("last_name present in the payload request")
				}
				return &lambda.InvokeOutput{
					Payload:    []byte(`{"message":"Hello  !"}`),
					StatusCode: 200,
				}, nil
			})
		},
	)

	r := &proxy.Request{
		Params: map[string]string{"function": "python37"},
		Body:   ioutil.NopCloser(bytes.NewBufferString(`"first_name":"foobar","last_name":"some"}`)),
	}
	remote := &config.Backend{
		Method: "POST",
		ExtraConfig: config.ExtraConfig{
			Namespace: map[string]interface{}{},
		},
	}
	resp, err := bf(remote)(context.Background(), r)
	if err == nil {
		t.Error("error expected")
		return
	}
	if resp != nil {
		t.Errorf("unexpected response: %v", resp)
		return
	}
}

func TestBackendFactoryWithInvoker_incomplete(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := BackendFactoryWithInvoker(
		logging.NoOp,
		explosiveBF,
		func(_ *Options) Invoker {
			return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
				if in.InvocationType != types.InvocationTypeRequestResponse {
					t.Errorf("unexpected InvocationType: %s", in.InvocationType)
				}
				// if *in.ClientContext != clientContext {
				// 	t.Errorf("unexpected ClientContext: %s", *in.ClientContext)
				// }
				if *in.FunctionName != "" {
					t.Errorf("unexpected FunctionName: %s", *in.FunctionName)
				}
				payload := map[string]string{}
				if err := json.Unmarshal(in.Payload, &payload); err != nil {
					t.Error(err)
					return nil, err
				}
				if payload["first_name"] != "" {
					t.Errorf("first_name present in the payload request")
				}
				if payload["last_name"] != "" {
					t.Errorf("last_name present in the payload request")
				}
				return &lambda.InvokeOutput{
					Payload:    []byte(`{"message":"Hello  !"}`),
					StatusCode: 200,
				}, nil
			})
		},
	)

	remote := &config.Backend{
		Method: "GET",
		ExtraConfig: config.ExtraConfig{
			Namespace: map[string]interface{}{},
		},
	}
	resp, err := bf(remote)(context.Background(), &proxy.Request{})
	if err != nil {
		t.Error(err)
		return
	}
	if resp == nil {
		t.Errorf("unexpected response: %v", resp)
		return
	}
	if !resp.IsComplete {
		t.Error("incomplete response")
		return
	}
	if m, ok := resp.Data["message"]; !ok || m != "Hello  !" {
		t.Errorf("unexpected response: %v", resp.Data)
		return
	}
}

func TestBackendFactoryWithInvoker_wrongStatusCode(t *testing.T) {
	explosiveBF := func(remote *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	}

	bf := BackendFactoryWithInvoker(
		logging.NoOp,
		explosiveBF,
		func(_ *Options) Invoker {
			return invoker(func(in *lambda.InvokeInput) (*lambda.InvokeOutput, error) {
				return &lambda.InvokeOutput{
					Payload:    []byte(``),
					StatusCode: 404,
				}, nil
			})
		},
	)

	remote := &config.Backend{
		Method: "GET",
		ExtraConfig: config.ExtraConfig{
			Namespace: map[string]interface{}{},
		},
	}
	resp, err := bf(remote)(context.Background(), &proxy.Request{})
	if err != errBadStatusCode {
		t.Error(err)
		return
	}
	if resp != nil {
		t.Errorf("unexpected response: %v", resp)
		return
	}
}

type invoker func(*lambda.InvokeInput) (*lambda.InvokeOutput, error)

func (i invoker) Invoke(_ context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	return i(in)
}

func TestBackendFactory_sdkClient(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/2015-03-31/functions/python37/invocations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if h := r.Header.Get("X-Amz-Invocation-Type"); h != "RequestResponse" {
			t.Errorf("unexpected invocation type: %s", h)
		}
		if a := r.Header.Get("Authorization"); !strings.Contains(a, "/us-east-1/lambda/") {
			t.Errorf("unexpected signature scope: %s", a)
		}
		w.Header().Set("X-Amz-Executed-Version", "$LATEST")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message":"Hello fooo bar!"}`))
	}))
	defer srv.Close()

	bf := BackendFactory(logging.NoOp, func(_ *config.Backend) proxy.Proxy {
		t.Error("this backend factory should not been called")
		return proxy.NoopProxy
	})

	remote := &config.Backend{
		Method: "GET",
		ExtraConfig: config.ExtraConfig{
			Namespace: map[string]interface{}{
				"function_name": "python37",
				"region":        "us-east-1",
				"endpoint":      srv.URL,
			},
		},
	}
	resp, err := bf(remote)(context.Background(), &proxy.Request{
		Params: map[string]string{"first_name": "fooo", "last_name": "bar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m := resp.Data["message"]; m != "Hello fooo bar!" {
		t.Errorf("unexpected response: %v", resp.Data)
	}
	if v := resp.Metadata.Headers["X-Amz-Executed-Version"]; len(v) != 1 || v[0] != "$LATEST" {
		t.Errorf("unexpected executed version header: %v", v)
	}
}

func TestGetOptions_maxRetries(t *testing.T) {
	for _, tc := range []struct {
		Name     string
		Value    interface{}
		Expected int
	}{
		{Name: "json float", Value: float64(3), Expected: 4},
		{Name: "json zero", Value: float64(0), Expected: 1},
		{Name: "int", Value: 2, Expected: 3},
		{Name: "int64", Value: int64(5), Expected: 6},
		{Name: "json number", Value: json.Number("7"), Expected: 8},
		{Name: "fractional", Value: 1.5, Expected: 0},
		{Name: "negative", Value: float64(-1), Expected: 0},
		{Name: "string", Value: "3", Expected: 0},
		{Name: "missing", Value: nil, Expected: 0},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			extra := map[string]interface{}{"region": "us-east-1"}
			if tc.Value != nil {
				extra["max_retries"] = tc.Value
			}
			opts, err := getOptions(&config.Backend{
				Method:      "GET",
				ExtraConfig: config.ExtraConfig{Namespace: extra},
			})
			if err != nil {
				t.Fatal(err)
			}
			if opts.Config.RetryMaxAttempts != tc.Expected {
				t.Errorf("unexpected RetryMaxAttempts: got %d, want %d", opts.Config.RetryMaxAttempts, tc.Expected)
			}
		})
	}
}

func TestGetOptions_maxRetriesFromJSON(t *testing.T) {
	cfg := config.ExtraConfig{}
	if err := json.Unmarshal([]byte(`{"backend/lambda":{"region":"us-east-1","max_retries":2}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	opts, err := getOptions(&config.Backend{Method: "GET", ExtraConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Config.RetryMaxAttempts != 3 {
		t.Errorf("unexpected RetryMaxAttempts: %d", opts.Config.RetryMaxAttempts)
	}
}
