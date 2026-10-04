//go:build js && wasm

// Command wasm is the engine's browser build. It runs one duel at a time
// through package bridge and exposes it to JavaScript as globalThis.duelEngine:
//
//	version                      the engine build (fork commit)
//	newDuel(specJson, onMessage) starts a duel and returns {ok, report} or {ok: false, error};
//	                             onMessage(seat, json) receives every outgoing message
//	                             (seat 1 or 2, or 0 for the bridge's own)
//	send(seat, json)             queues a seat's input; returns null or an error string
//	settled()                    true when every input has been dealt with
//	debugState()                 the whole duel, nothing hidden, as JSON
//	position()                   the duel's current position as a BoardSetup, as JSON:
//	                             {ok: true, position} when the duel has settled with nobody
//	                             mid-answer, else {ok: false, error} saying why
//	inputs()                     every delivered input, in order, as JSON
//	result()                     how the duel ended, as JSON
//	engineCards()                every card id the engine can play, as JSON
//	close()                      ends the current duel
//
// JSON goes both ways as strings, so the page parses it with JSON.parse.
package main

import (
	"duel-masters/bridge"
	"duel-masters/game/match"
	"encoding/json"
	"os"
	"strings"
	"syscall/js"

	"github.com/sirupsen/logrus"
)

var current *bridge.Duel

func main() {
	switch strings.ToLower(os.Getenv("log_level")) {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "info":
		logrus.SetLevel(logrus.InfoLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.WarnLevel)
	}

	api := js.Global().Get("Object").New()
	api.Set("version", bridge.Version)
	api.Set("newDuel", js.FuncOf(newDuel))
	api.Set("send", js.FuncOf(send))
	api.Set("settled", js.FuncOf(func(js.Value, []js.Value) any {
		return current != nil && current.Settled()
	}))
	api.Set("debugState", js.FuncOf(func(js.Value, []js.Value) any {
		if current == nil {
			return js.Null()
		}
		return toJSON(current.DebugState())
	}))
	api.Set("position", js.FuncOf(func(js.Value, []js.Value) any {
		if current == nil {
			return failure("there is no duel running")
		}
		position, err := current.Position()
		if err != nil {
			return failure(err.Error())
		}
		return toJSON(struct {
			OK       bool              `json:"ok"`
			Position *match.BoardSetup `json:"position"`
		}{true, position})
	}))
	api.Set("inputs", js.FuncOf(func(js.Value, []js.Value) any {
		if current == nil {
			return "[]"
		}
		return toJSON(current.Inputs())
	}))
	api.Set("result", js.FuncOf(func(js.Value, []js.Value) any {
		if current == nil {
			return js.Null()
		}
		return toJSON(current.Result())
	}))
	api.Set("engineCards", js.FuncOf(func(js.Value, []js.Value) any {
		return toJSON(bridge.EngineCards())
	}))
	api.Set("close", js.FuncOf(func(js.Value, []js.Value) any {
		closeCurrent()
		return nil
	}))

	js.Global().Set("duelEngine", api)

	// Keep the program, and so the engine, alive for the page's lifetime.
	select {}
}

func newDuel(_ js.Value, args []js.Value) any {
	closeCurrent()

	if len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeFunction {
		return failure("newDuel needs the duel spec as a JSON string and a message callback")
	}

	spec, err := bridge.ParseSpec([]byte(args[0].String()))
	if err != nil {
		return failure(err.Error())
	}

	onMessage := args[1]
	duel, report, err := bridge.New(spec, func(seat int, msg []byte) {
		onMessage.Invoke(seat, string(msg))
	})
	if err != nil {
		return failure(err.Error())
	}

	current = duel
	return toJSON(struct {
		OK     bool           `json:"ok"`
		Report *bridge.Report `json:"report"`
	}{true, report})
}

func send(_ js.Value, args []js.Value) any {
	if current == nil {
		return "there is no duel running"
	}
	if len(args) < 2 || args[0].Type() != js.TypeNumber || args[1].Type() != js.TypeString {
		return "send needs a seat number and the input as a JSON string"
	}
	if err := current.Send(args[0].Int(), []byte(args[1].String())); err != nil {
		return err.Error()
	}
	return js.Null()
}

func closeCurrent() {
	if current != nil {
		current.Close()
		current = nil
	}
}

func failure(message string) any {
	return toJSON(struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}{false, message})
}

func toJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `null`
	}
	return string(data)
}
