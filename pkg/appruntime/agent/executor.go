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

package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"
	"github.com/tmc/langchaingo/tools/scraper"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubeagi/arcadia/api/app-node/agent/v1alpha1"
	"github.com/kubeagi/arcadia/pkg/appruntime/base"
	"github.com/kubeagi/arcadia/pkg/tools/weather"
)

type Executor struct {
	base.BaseNode
}

func NewExecutor(baseNode base.BaseNode) *Executor {
	return &Executor{
		baseNode,
	}
}

func (p *Executor) Run(ctx context.Context, cli client.Client, args map[string]any) (map[string]any, error) {
	v1, ok := args["llm"]
	if !ok {
		return args, errors.New("no llm")
	}
	llm, ok := v1.(llms.LLM)
	if !ok {
		return args, errors.New("llm not llms.LanguageModel")
	}
	instance := &v1alpha1.Agent{}
	if err := cli.Get(ctx, types.NamespacedName{Namespace: p.RefNamespace(), Name: p.Ref.Name}, instance); err != nil {
		return args, fmt.Errorf("can't find the agent in cluster: %w", err)
	}
	var allowedTools []tools.Tool
	// prepare tools that can be used by this agent
	for _, toolSpec := range instance.Spec.AllowedTools {
		switch toolSpec.Name {
		case weather.ToolName:
			tool, err := weather.New(&toolSpec)
			if err != nil {
				klog.Errorf("failed to create a new weather tool:", err)
				continue
			}
			allowedTools = append(allowedTools, tool)
		case "calculator":
			tool := tools.Calculator{}
			allowedTools = append(allowedTools, tool)
		case "scraper":
			// prepare options from toolSpec
			options := make([]scraper.Options, 0)
			if toolSpec.Params["delay"] != "" {
				delay, err := strconv.ParseInt(toolSpec.Params["delay"], 10, 64)
				if err != nil {
					klog.Errorln("failed to parse delay %s", toolSpec.Params["delay"])
				} else {
					options = append(options, scraper.WithDelay(delay))
				}
			}
			if toolSpec.Params["async"] != "" {
				async, err := strconv.ParseBool(toolSpec.Params["async"])
				if err != nil {
					klog.Errorln("failed to parse async %s", toolSpec.Params["async"])
				} else {
					options = append(options, scraper.WithAsync(async))
				}
			}
			if toolSpec.Params["handleLinks"] != "" {
				handleLinks, err := strconv.ParseBool(toolSpec.Params["handleLinks"])
				if err != nil {
					klog.Errorln("failed to parse handleLinks %s", toolSpec.Params["handleLinks"])
				} else {
					options = append(options, scraper.WithHandleLinks(handleLinks))
				}
			}
			if toolSpec.Params["blacklist"] != "" {
				blacklistArray := strings.Split(toolSpec.Params["blacklist"], ",")
				options = append(options, scraper.WithBlacklist(blacklistArray))
			}
			tool, err := scraper.New(options...)
			if err != nil {
				klog.Errorf("failed to create a new scraper tool:", err)
				continue
			}
			allowedTools = append(allowedTools, tool)
		default:
			return nil, fmt.Errorf("no tool found with name: %s", toolSpec.Name)
		}
	}

	// Initialize executor using langchaingo
	executorOptions := func(o *agents.CreationOptions) {
		agents.WithMaxIterations(instance.Spec.Options.MaxIterations)(o)
		// Only show tool action in the streaming output if configured
		if instance.Spec.Options.ShowToolAction {
			if needStream, ok := args["_need_stream"].(bool); ok && needStream {
				streamHandler := StreamHandler{callbacks.SimpleHandler{}, args}
				agents.WithCallbacksHandler(streamHandler)(o)
			}
		}
	}
	executor, err := agents.Initialize(llm, allowedTools, agents.ZeroShotReactDescription, executorOptions)
	if err != nil {
		return args, fmt.Errorf("failed to initialize executor: %w", err)
	}
	input := make(map[string]any)
	input["input"] = args["question"]
	response, err := executor.Call(ctx, input)
	if err != nil {
		return args, fmt.Errorf("error when call agent: %w", err)
	}
	klog.FromContext(ctx).V(5).Info("use agent, blocking out:", response["output"])
	if err == nil {
		args["_answer"] = response["output"]
		return args, nil
	}
	return args, nil
}
