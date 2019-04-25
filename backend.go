package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/devopsfaith/krakend/config"
	"github.com/devopsfaith/krakend/proxy"
)

const (
	Namespace = "github.com/devopsfaith/krakend-lambda"
)

var (
	errBadStatusCode = errors.New("aws lambda: bad status code")
	errNoConfig      = errors.New("aws lambda: no extra config defined")
	errBadConfig     = errors.New("aws lambda: unable to parse the defined extra config")
)

type Invoker interface {
	Invoke(*lambda.InvokeInput) (*lambda.InvokeOutput, error)
}

func BackendFactory(bf proxy.BackendFactory) proxy.BackendFactory {
	return BackendFactoryWithInvoker(bf, lambda.New(session.New()))
}

func BackendFactoryWithInvoker(bf proxy.BackendFactory, i Invoker) proxy.BackendFactory {
	return func(remote *config.Backend) proxy.Proxy {
		ecfg, err := getOptions(remote)
		if err != nil {
			return bf(remote)
		}

		return func(ctx context.Context, r *proxy.Request) (*proxy.Response, error) {
			payload, err := ecfg.PayloadExtractor(r)
			if err != nil {
				return nil, err
			}
			input := &lambda.InvokeInput{
				ClientContext:  aws.String("MyApp"),
				FunctionName:   aws.String(ecfg.FunctionExtractor(r)),
				InvocationType: aws.String("RequestResponse"),
				LogType:        aws.String("Tail"),
				Payload:        payload,
				// Qualifier:      aws.String("1"),
			}

			result, err := i.Invoke(input)
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
			response := &proxy.Response{
				Metadata: proxy.Metadata{
					StatusCode: int(*result.StatusCode),
					Headers:    map[string][]string{},
				},
				Data:       data,
				IsComplete: true,
			}

			if result.ExecutedVersion != nil {
				response.Metadata.Headers["X-Amz-Executed-Version"] = []string{*result.ExecutedVersion}
			}

			return response, nil
		}
	}
}

func getOptions(remote *config.Backend) (*options, error) {
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

	cfg := &options{
		FunctionExtractor: funcExtractor,
	}
	if remote.Method == "GET" {
		cfg.PayloadExtractor = fromParams
	} else {
		cfg.PayloadExtractor = fromBody
	}
	return cfg, nil
}

type options struct {
	PayloadExtractor  payloadExtractor
	FunctionExtractor functionExtractor
}

type functionExtractor func(*proxy.Request) string

type payloadExtractor func(*proxy.Request) ([]byte, error)

func fromParams(r *proxy.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(r.Params)
	return buf.Bytes(), err
}

func fromBody(r *proxy.Request) ([]byte, error) {
	return ioutil.ReadAll(r.Body)
}
