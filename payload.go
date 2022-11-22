package lambda

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/luraproject/lura/v2/proxy"
)

func payloadExtractorFactory(httpMethod string, ecfg map[string]interface{}) payloadExtractor {
	isApiGatewayFormatEnabled := false
	awsApigwFormatCfg, ok := ecfg["aws_api_gateway_format"].(map[string]interface{})
	if ok {
		if awsApigwFormatCfgEnabled, ok := awsApigwFormatCfg["enabled"]; ok && awsApigwFormatCfgEnabled.(bool) {
			isApiGatewayFormatEnabled = true
		}
	}
	if isApiGatewayFormatEnabled {
		return fromAwsApiGatewayV1Format
	}
	if httpMethod == "GET" {
		return fromParams
	}
	return fromBody
}

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

// apiGatewayRequestIdentity contains identity information for the request caller.
type apiGatewayRequestIdentity struct {
	UserAgent string `json:"userAgent"`
}

type apiGatewayProxyRequestContext struct {
	Path       string                    `json:"path"`
	Protocol   string                    `json:"protocol"`
	HTTPMethod string                    `json:"httpMethod"`
	Identity   apiGatewayRequestIdentity `json:"identity"`
}

// apiGatewayProxyRequest contains data coming from the API Gateway proxy
type apiGatewayProxyRequest struct {
	Version                         string                        `json:"version"`
	Path                            string                        `json:"path"` // The url path for the caller
	HTTPMethod                      string                        `json:"httpMethod"`
	Headers                         map[string]string             `json:"headers"`
	MultiValueHeaders               map[string][]string           `json:"multiValueHeaders"`
	QueryStringParameters           map[string]string             `json:"queryStringParameters"`
	MultiValueQueryStringParameters map[string][]string           `json:"multiValueQueryStringParameters"`
	PathParameters                  map[string]string             `json:"pathParameters"`
	RequestContext                  apiGatewayProxyRequestContext `json:"requestContext"`
	Body                            string                        `json:"body"`
}

func fromAwsApiGatewayV1Format(r *proxy.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	singleValueHeaders := make(map[string]string)
	for headerKey, headers := range r.Headers {
		singleValueHeaders[headerKey] = headers[0]
	}
	singleValueQuery := make(map[string]string)
	for queryKey, query := range r.Query {
		singleValueQuery[queryKey] = query[0]
	}
	userAgent := singleValueHeaders["User-Agent"]
	payload := apiGatewayProxyRequest{
		Version:                         "1.0",
		Path:                            r.Path,
		HTTPMethod:                      r.Method,
		Headers:                         singleValueHeaders,
		MultiValueHeaders:               r.Headers,
		QueryStringParameters:           singleValueQuery,
		MultiValueQueryStringParameters: r.Query,
		PathParameters:                  r.Params,
		RequestContext: apiGatewayProxyRequestContext{
			Path:       r.Path,
			Protocol:   r.URL.Scheme,
			HTTPMethod: r.Method,
			Identity: apiGatewayRequestIdentity{
				UserAgent: userAgent,
			},
		},
		Body: string(body),
	}
	return json.Marshal(payload)
}
