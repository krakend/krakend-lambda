package lambda

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/ioutil"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/luraproject/lura/config"
	"github.com/luraproject/lura/core"
	"github.com/luraproject/lura/proxy"
)

const (
	Namespace = "github.com/devopsfaith/krakend-lambda"
)

var (
	errBadStatusCode = errors.New("aws lambda: bad status code")
	errNoConfig      = errors.New("aws lambda: no extra config defined")
	errBadConfig     = errors.New("aws lambda: unable to parse the defined extra config")
	clientContext    = base64.StdEncoding.EncodeToString([]byte(`{"client":"KrakenD", "version":"` + core.KrakendVersion + `"}`))
)

type Invoker interface {
	InvokeWithContext(aws.Context, *lambda.InvokeInput, ...request.Option) (*lambda.InvokeOutput, error)
}

func BackendFactory(bf proxy.BackendFactory) proxy.BackendFactory {
	return BackendFactoryWithInvoker(bf, invokerFactory)
}

func invokerFactory(o *Options) Invoker {
	if o.Config == nil {
		return lambda.New(session.New())
	}
	return lambda.New(session.Must(session.NewSession(o.Config)))
}

func BackendFactoryWithInvoker(bf proxy.BackendFactory, invokerFactory func(*Options) Invoker) proxy.BackendFactory {
	return func(remote *config.Backend) proxy.Proxy {
		ecfg, err := getOptions(remote)
		if err != nil {
			return bf(remote)
		}

		i := invokerFactory(ecfg)

		ef := proxy.NewEntityFormatter(remote)

		return func(ctx context.Context, r *proxy.Request) (*proxy.Response, error) {
			payload, err := ecfg.PayloadExtractor(r)
			if err != nil {
				return nil, err
			}
			input := &lambda.InvokeInput{
				// ClientContext:  aws.String(clientContext),
				FunctionName:   aws.String(ecfg.FunctionExtractor(r)),
				InvocationType: aws.String("RequestResponse"),
				LogType:        aws.String("Tail"),
				Payload:        payload,
				// Qualifier:      aws.String("1"),
			}

			result, err := i.InvokeWithContext(ctx, input)
			if err != nil {
				return nil, err
			}
			if result.StatusCode == nil || *result.StatusCode != 200 {
				return nil, errBadStatusCode
			}

			data := map[string]interface{}{}
			if err := json.Unmarshal(result.Payload, &data); err != nil {
				return nil, err
			}
			response := ef.Format(proxy.Response{
				Metadata: proxy.Metadata{
					StatusCode: int(*result.StatusCode),
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
		Region: aws.String(region),
	}

	if endpoint, ok := ecfg["endpoint"].(string); ok {
		cfg.Config.WithEndpoint(endpoint)
	}

	if retries, ok := ecfg["max_retries"].(int); ok {
		cfg.Config.WithMaxRetries(retries)
	}

	return cfg, nil
}

type Options struct {
	PayloadExtractor  payloadExtractor
	FunctionExtractor functionExtractor
	Config            *aws.Config
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
	return ioutil.ReadAll(r.Body)
}
