/*
Copyright 2024 KubeAGI.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package weather

import (
	"context"
	"strings"

	"github.com/kubeagi/arcadia/api/app-node/agent/v1alpha1"
	"github.com/kubeagi/arcadia/pkg/appruntime/tools/weather/internal"
	"github.com/tmc/langchaingo/tools"
)

const (
	ToolName = "Weather Query API"
)

type Tool struct {
	client *internal.Client
}

var _ tools.Tool = Tool{}

// New creates a new weather tool to search on internet
func New(tool *v1alpha1.Tool) (*Tool, error) {
	return &Tool{
		client: internal.New(tool.Parms["apiKey"]),
	}, nil
}

func (t Tool) Name() string {
	return ToolName
}

func (t Tool) Description() string {
	return "Invoke API to get the realtime weather data."
}

func (t Tool) Call(ctx context.Context, input string) (string, error) {
	result, err := t.client.GetData(ctx, input)
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(result), " "), nil
}
