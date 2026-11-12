// Package lambda provides a KrakenD backend that invokes AWS Lambda functions
// instead of making HTTP requests.
//
// To enable it, add the Namespace key to the extra_config of a backend. It
// accepts these options:
//
//   - function_name: the name of the function to invoke. If it is not set,
//     the name is taken from the request param named by function_param_name.
//   - function_param_name: the request param that holds the function name
//     when function_name is not set. Defaults to "function".
//   - region: the AWS region of the function. If it is not set, the AWS SDK
//     default configuration chain picks the region, and endpoint and
//     max_retries are ignored.
//   - endpoint: a custom base endpoint for the Lambda API, such as a
//     LocalStack instance.
//   - max_retries: the maximum number of retries for each invocation.
//
// For GET backends, the payload is a JSON object built from the request
// params, with lowercased keys. For any other method, the request body is
// sent as the payload unchanged.
package lambda

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/luraproject/lura/v3/config"
	"github.com/luraproject/lura/v3/core"
	"github.com/luraproject/lura/v3/logging"
	"github.com/luraproject/lura/v3/proxy"
)

const (
	// Namespace is the key to use in the extra_config of a backend to
	// enable and configure this component.
	Namespace = "backend/lambda"
)

var (
	errBadStatusCode = errors.New("aws lambda: bad status code")
	errNoConfig      = errors.New("aws lambda: no extra config defined")
	errBadConfig     = errors.New("aws lambda: unable to parse the defined extra config")
	clientContext    = base64.StdEncoding.EncodeToString([]byte(`{"client":"KrakenD", "version":"` + core.KrakendVersion + `"}`))
)

// Invoker invokes AWS Lambda functions. It is satisfied by *lambda.Client from
// the AWS SDK v2, and can be replaced by a custom implementation (a mock in
// tests, for example) through BackendFactoryWithInvoker.
type Invoker interface {
	Invoke(context.Context, *lambda.InvokeInput, ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// BackendFactory returns a proxy.BackendFactory that invokes AWS Lambda
// functions for the backends that have the Namespace key in their
// extra_config. Every other backend, and any backend whose configuration
// cannot be parsed, is built by the fallback factory bf.
//
// The Lambda client is created from the AWS SDK v2 default configuration
// (environment variables, shared config files, instance roles...), with the
// region, endpoint and retries of the backend configuration on top.
func BackendFactory(logger logging.Logger, bf proxy.BackendFactory) proxy.BackendFactory {
	return BackendFactoryWithInvoker(logger, bf, invokerFactory)
}

func invokerFactory(o *Options) Invoker {
	var optFns []func(*awsconfig.LoadOptions) error
	if o.Config != nil {
		if o.Config.Region != "" {
			optFns = append(optFns, awsconfig.WithRegion(o.Config.Region))
		}
		if o.Config.RetryMaxAttempts > 0 {
			optFns = append(optFns, awsconfig.WithRetryMaxAttempts(o.Config.RetryMaxAttempts))
		}
		if o.Config.BaseEndpoint != nil {
			optFns = append(optFns, awsconfig.WithBaseEndpoint(*o.Config.BaseEndpoint))
		}
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), optFns...)
	if err != nil {
		return failingInvoker{err: err}
	}
	return lambda.NewFromConfig(cfg)
}

type failingInvoker struct {
	err error
}

func (f failingInvoker) Invoke(_ context.Context, _ *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	return nil, f.err
}

// BackendFactoryWithInvoker works like BackendFactory, but it uses
// invokerFactory to create the Invoker of each backend from its parsed
// Options, so the Lambda client can be customized or replaced.
//
// The returned proxies invoke the function synchronously (RequestResponse)
// and fail with an error unless the invocation returns a 200 status code. The
// function result must be a JSON object; it is used as the response data,
// after applying the backend's filtering and mapping options. When the result
// includes an executed version, it is copied to the X-Amz-Executed-Version
// response header.
func BackendFactoryWithInvoker(logger logging.Logger, bf proxy.BackendFactory, invokerFactory func(*Options) Invoker) proxy.BackendFactory {
	return func(remote *config.Backend) proxy.Proxy {
		logPrefix := fmt.Sprintf("[BACKEND: %s %s -> %s %s][Lambda]",
			remote.ParentEndpointMethod, remote.ParentEndpoint,
			remote.Method, remote.URLPattern)
		ecfg, err := getOptions(remote)
		if err != nil {
			if err != errNoConfig {
				logger.Error(logPrefix, err)
			}
			return bf(remote)
		}

		i := invokerFactory(ecfg)

		ef := proxy.NewEntityFormatter(remote)

		logger.Debug(logPrefix, "Component enabled")

		return func(ctx context.Context, r *proxy.Request) (*proxy.Response, error) {
			payload, err := ecfg.PayloadExtractor(r)
			if err != nil {
				return nil, err
			}
			input := &lambda.InvokeInput{
				// ClientContext:  aws.String(clientContext),
				FunctionName:   aws.String(ecfg.FunctionExtractor(r)),
				InvocationType: types.InvocationTypeRequestResponse,
				LogType:        types.LogTypeNone,
				Payload:        payload,
				// Qualifier:      aws.String("1"),
			}

			result, err := i.Invoke(ctx, input)
			if err != nil {
				return nil, err
			}
			if result.StatusCode != 200 {
				return nil, errBadStatusCode
			}

			data := map[string]interface{}{}
			if err := json.Unmarshal(result.Payload, &data); err != nil {
				return nil, err
			}
			response := ef.Format(proxy.Response{
				Metadata: proxy.Metadata{
					StatusCode: int(result.StatusCode),
					Headers:    map[string][]string{},
				},
				Data:       data,
				IsComplete: true,
			})

			if result.ExecutedVersion != nil {
				response.Metadata.Headers["X-Amz-Executed-Version"] = []string{*result.ExecutedVersion}
			}

			return &response, nil
		}
	}
}

func getOptions(remote *config.Backend) (*Options, error) {
	v, ok := remote.ExtraConfig[Namespace]
	if !ok {
		return nil, errNoConfig
	}
	ecfg, ok := v.(map[string]interface{})
	if !ok {
		return nil, errBadConfig
	}

	var funcExtractor functionExtractor
	funcName, ok := ecfg["function_name"].(string)
	if ok {
		funcExtractor = func(_ *proxy.Request) string {
			return funcName
		}
	} else {
		funcParamName, ok := ecfg["function_param_name"].(string)
		if !ok {
			funcParamName = "function"
		}
		funcExtractor = func(r *proxy.Request) string {
			return r.Params[funcParamName]
		}
	}

	cfg := &Options{
		FunctionExtractor: funcExtractor,
	}
	if remote.Method == "GET" {
		cfg.PayloadExtractor = fromParams
	} else {
		cfg.PayloadExtractor = fromBody
	}

	region, ok := ecfg["region"].(string)
	if !ok {
		return cfg, nil
	}

	cfg.Config = &aws.Config{
		Region: region,
	}

	if endpoint, ok := ecfg["endpoint"].(string); ok {
		cfg.Config.BaseEndpoint = aws.String(endpoint)
	}

	if retries, ok := parseRetries(ecfg["max_retries"]); ok {
		// the SDK v2 counts the total number of attempts, not the retries
		cfg.Config.RetryMaxAttempts = retries + 1
	}

	return cfg, nil
}

// parseRetries accepts the numeric types that a parsed configuration can hold
// (float64 when it comes from JSON) and only returns non-negative integers.
func parseRetries(v interface{}) (int, bool) {
	var retries int
	switch n := v.(type) {
	case int:
		retries = n
	case int64:
		retries = int(n)
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		retries = int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		retries = int(i)
	default:
		return 0, false
	}
	return retries, retries >= 0
}

// Options holds the parsed configuration of a Lambda backend.
type Options struct {
	// PayloadExtractor builds the invocation payload from the request.
	PayloadExtractor payloadExtractor
	// FunctionExtractor returns the name of the function to invoke for the
	// request.
	FunctionExtractor functionExtractor
	// Config holds the region, base endpoint and maximum attempts of the
	// AWS client. It is nil when no region is configured.
	Config *aws.Config
}

type functionExtractor func(*proxy.Request) string

type payloadExtractor func(*proxy.Request) ([]byte, error)

func fromParams(r *proxy.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	params := map[string]string{}
	for k, v := range r.Params {
		params[strings.ToLower(k)] = v
	}
	err := json.NewEncoder(buf).Encode(params)
	return buf.Bytes(), err
}

func fromBody(r *proxy.Request) ([]byte, error) {
	return io.ReadAll(r.Body)
}
