package cli

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"errors"
	"fmt"
	"strings"
)

type configReply struct {
	Sources  []corpus.Source `json:"sources,omitempty"`
	Rules    []string        `json:"rules,omitempty"`
	Count    int             `json:"count"`
	Total    int             `json:"total"`
	Empty    bool            `json:"empty"`
	Changed  bool            `json:"changed"`
	Next     []string        `json:"next"`
	NextArgv [][]string      `json:"next_argv"`
}

func (call *call) configWriteError(err error) int {
	if errors.Is(err, platform.ErrLocked) {
		return call.failure("config-locked", "Another process is editing this config. Retry the command.", 1, call.again())
	}
	return call.failure("config-unwritable", "The selected config cannot be written safely. Check its file and parent permissions.", 1, []string{"help", call.command})
}

func (call *call) manage() int {
	if len(call.words) == 0 {
		return call.usage("missing-argument", "Choose list, add, remove or move.")
	}
	action := call.words[0]
	count := map[string]int{"list": 1, "add": 2, "remove": 2, "move": 3}[action]
	if count == 0 || len(call.words) != count || action == "move" && call.command != "sources" {
		return call.usage("invalid-value", "Use the arguments shown in command help.")
	}
	if _, ok := call.flags["--project"]; ok && action != "add" {
		return call.usage("invalid-value", "--project applies only to sources add.")
	}
	var doc corpus.Document
	var changed bool
	var err error
	if action == "list" {
		doc, err = corpus.ReadDocument(call.config)
	} else {
		op := corpus.Operation{Collection: call.command, Action: action, Value: call.words[1], Project: call.flags["--project"]}
		if action == "move" {
			op.Target = call.words[2]
		}
		doc, changed, err = corpus.Edit(call.config, op)
	}
	if err != nil {
		if errors.Is(err, corpus.ErrOperation) {
			return call.usage("invalid-value", strings.TrimPrefix(err.Error(), corpus.ErrOperation.Error()+"\n"))
		}
		if errors.Is(err, corpus.ErrConfigMissing) || errors.Is(err, corpus.ErrConfigInvalid) {
			return call.configError(err)
		}
		return call.configWriteError(err)
	}
	reply := configReply{Changed: changed, Next: []string{}, NextArgv: [][]string{}}
	if call.command == "sources" {
		reply.Sources = doc.Config.Sources
		reply.Count = len(reply.Sources)
	} else {
		reply.Rules = doc.Config.Exclude
		if call.command == "third-party" {
			reply.Rules = doc.Config.ThirdParty
		}
		reply.Count = len(reply.Rules)
	}
	reply.Total, reply.Empty = reply.Count, reply.Count == 0
	if action != "list" {
		next, argv := call.next("index")
		reply.Next = append(reply.Next, next)
		reply.NextArgv = append(reply.NextArgv, argv)
	}
	if call.flags["--format"] == "json" {
		value := map[string]any{"count": reply.Count, "total": reply.Total, "empty": reply.Empty, "changed": reply.Changed, "next": reply.Next, "next_argv": reply.NextArgv}
		if call.command == "sources" {
			value["sources"] = append([]corpus.Source{}, reply.Sources...)
		} else {
			value["rules"] = append([]string{}, reply.Rules...)
		}
		if WriteJSON(call.output, value) != 0 {
			return 1
		}
	} else {
		text := fmt.Sprintf("%d %s entries. Changed: %t.\n", reply.Count, call.command, changed)
		for _, source := range reply.Sources {
			text += fmt.Sprintf("%d. %s", *source.SourceID, source.Path)
			if source.Project != "" {
				text += " (" + source.Project + ")"
			}
			text += "\n"
		}
		for i, rule := range reply.Rules {
			text += fmt.Sprintf("%d. %s\n", i+1, rule)
		}
		for _, next := range reply.Next {
			text += "Next: " + textCommand(next) + "\n"
		}
		if _, err := writeText(call.output, text); err != nil {
			return 1
		}
	}
	if action != "list" && !changed {
		return 3
	}
	return 0
}
