package ast_test

import (
	"context"
	"testing"

	"github.com/jenaiz/pcke/internal/analysis/ast"
)

func TestParseGoEntities(t *testing.T) {
	src := []byte(`package main

import (
	"fmt"
	"os"
)

// MaxRetries is the default retry limit.
const MaxRetries = 3

const internalLimit = 5

// Server handles HTTP requests.
type Server struct {
	addr string
}

type Handler interface {
	Handle(req Request) error
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) Start() error {
	return nil
}

func helper() {}
`)

	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseBytes(context.Background(), src, ast.LangGo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	assertGoEntities(t, result)
	assertGoImports(t, result)
}

func assertGoEntities(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if result.Language != "Go" {
		t.Errorf("language = %q, want Go", result.Language)
	}

	wantEntities := []struct {
		kind     ast.EntityKind
		name     string
		exported bool
	}{
		{ast.KindConstant, "MaxRetries", true},
		{ast.KindConstant, "internalLimit", false},
		{ast.KindStruct, "Server", true},
		{ast.KindInterface, "Handler", true},
		{ast.KindFunction, "NewServer", true},
		{ast.KindMethod, "Start", true},
		{ast.KindFunction, "helper", false},
	}

	if len(result.Entities) != len(wantEntities) {
		t.Fatalf("got %d entities, want %d:\n%v", len(result.Entities), len(wantEntities), entityNames(result.Entities))
	}

	for i, want := range wantEntities {
		got := result.Entities[i]
		if got.Kind != want.kind || got.Name != want.name || got.Exported != want.exported {
			t.Errorf("entity[%d] = {%s %q exported=%v}, want {%s %q exported=%v}",
				i, got.Kind, got.Name, got.Exported, want.kind, want.name, want.exported)
		}
	}

	// Check method receiver.
	startMethod := findEntity(result.Entities, "Start")
	if startMethod == nil || startMethod.Receiver != "Server" {
		t.Errorf("Start method receiver = %q, want Server", safeReceiver(startMethod))
	}
}

func assertGoImports(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if len(result.Imports) != 2 {
		t.Fatalf("got %d imports, want 2", len(result.Imports))
	}
	if result.Imports[0].Path != "fmt" {
		t.Errorf("import[0] = %q, want fmt", result.Imports[0].Path)
	}
	if result.Imports[1].Path != "os" {
		t.Errorf("import[1] = %q, want os", result.Imports[1].Path)
	}
}

func TestParsePythonEntities(t *testing.T) {
	src := []byte(`import os
from pathlib import Path
import json as j

def main():
    pass

class Server:
    def __init__(self, addr):
        self.addr = addr

    def start(self):
        pass

def _helper():
    pass
`)

	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseBytes(context.Background(), src, ast.LangPython)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	assertPythonEntities(t, result)
	assertPythonImports(t, result)
}

func assertPythonEntities(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if result.Language != "Python" {
		t.Errorf("language = %q, want Python", result.Language)
	}

	wantNames := []string{"main", "Server", "__init__", "start", "_helper"}
	if len(result.Entities) != len(wantNames) {
		t.Fatalf("got %d entities, want %d:\n%v", len(result.Entities), len(wantNames), entityNames(result.Entities))
	}
	for i, name := range wantNames {
		if result.Entities[i].Name != name {
			t.Errorf("entity[%d].Name = %q, want %q", i, result.Entities[i].Name, name)
		}
	}

	helper := findEntity(result.Entities, "_helper")
	if helper == nil || helper.Exported {
		t.Error("_helper should not be exported")
	}
}

func assertPythonImports(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if len(result.Imports) != 3 {
		t.Fatalf("got %d imports, want 3", len(result.Imports))
	}
	if result.Imports[0].Path != "os" {
		t.Errorf("import[0] = %q, want os", result.Imports[0].Path)
	}
	if result.Imports[1].Path != "pathlib" {
		t.Errorf("import[1] = %q, want pathlib", result.Imports[1].Path)
	}
}

func TestParseJavaScriptEntities(t *testing.T) {
	src := []byte(`import express from 'express';
import { Router } from 'express';

export function handleRequest(req, res) {
    res.send('ok');
}

export class Server {
    constructor(port) {
        this.port = port;
    }

    start() {
        console.log('started');
    }
}

const helper = () => {};

export const PORT = 3000;
`)

	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseBytes(context.Background(), src, ast.LangJavaScript)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	assertJSEntities(t, result)
	assertJSImports(t, result)
}

func assertJSEntities(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if result.Language != "JavaScript" {
		t.Errorf("language = %q, want JavaScript", result.Language)
	}

	// Should find: handleRequest (exported func), Server (exported class),
	// constructor (method), start (method), helper (non-exported arrow fn), PORT (exported const)
	if len(result.Entities) < 4 {
		t.Fatalf("got %d entities, want >= 4:\n%v", len(result.Entities), entityNames(result.Entities))
	}

	handleReq := findEntity(result.Entities, "handleRequest")
	if handleReq == nil || !handleReq.Exported {
		t.Error("handleRequest should be exported")
	}

	server := findEntity(result.Entities, "Server")
	if server == nil || server.Kind != ast.KindClass || !server.Exported {
		t.Error("Server should be an exported class")
	}

	port := findEntity(result.Entities, "PORT")
	if port == nil || !port.Exported {
		t.Error("PORT should be exported")
	}
}

func assertJSImports(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if len(result.Imports) != 2 {
		t.Fatalf("got %d imports, want 2", len(result.Imports))
	}
	if result.Imports[0].Path != "express" {
		t.Errorf("import[0] = %q, want express", result.Imports[0].Path)
	}
}

func TestParseTypeScript(t *testing.T) {
	src := []byte(`import { Request, Response } from 'express';

export interface Handler {
    handle(req: Request): Response;
}

export class Router implements Handler {
    handle(req: Request): Response {
        return {} as Response;
    }
}

export function createRouter(): Router {
    return new Router();
}
`)

	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseBytes(context.Background(), src, ast.LangTypeScript)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if result.Language != "TypeScript" {
		t.Errorf("language = %q, want TypeScript", result.Language)
	}

	iface := findEntity(result.Entities, "Handler")
	if iface == nil || iface.Kind != ast.KindInterface {
		t.Error("Handler should be an interface")
	}

	router := findEntity(result.Entities, "Router")
	if router == nil || router.Kind != ast.KindClass {
		t.Error("Router should be a class")
	}

	createRouter := findEntity(result.Entities, "createRouter")
	if createRouter == nil || createRouter.Kind != ast.KindFunction {
		t.Error("createRouter should be a function")
	}
}

func TestUnsupportedExtension(t *testing.T) {
	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseFile(context.Background(), "test.rb", ".rb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for unsupported extension")
	}
}

func TestParseJavaEntities(t *testing.T) {
	src := []byte(`package com.example;

import java.util.List;
import static java.util.Collections.emptyList;

/** Server handles HTTP requests. */
public class Server {
    private final String host;

    public static final int DEFAULT_PORT = 8080;

    public Server(String host) {
        this.host = host;
    }

    public void start() {
        System.out.println("started");
    }

    private void helper() {}

    public static class Builder {
        public Builder withHost(String h) { return this; }
    }
}

public interface Handler {
    void handle(Request req);
}

public enum Status {
    ACTIVE, INACTIVE, PENDING
}

public @interface Validated {
    String value() default "";
}
`)

	p := ast.NewParser()
	defer p.Close()

	result, err := p.ParseBytes(context.Background(), src, ast.LangJava)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	assertJavaEntities(t, result)
	assertJavaImports(t, result)
}

func assertJavaEntities(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if result.Language != "Java" {
		t.Errorf("language = %q, want Java", result.Language)
	}

	wantEntities := []struct {
		kind     ast.EntityKind
		name     string
		exported bool
	}{
		{ast.KindClass, "Server", true},
		{ast.KindConstant, "DEFAULT_PORT", true},
		{ast.KindMethod, "Server", true},        // constructor
		{ast.KindMethod, "start", true},         // public method
		{ast.KindMethod, "helper", false},       // private method
		{ast.KindClass, "Server.Builder", true}, // inner class
		{ast.KindMethod, "withHost", true},      // inner class method
		{ast.KindInterface, "Handler", true},
		{ast.KindMethod, "handle", false}, // interface method (no public modifier)
		{ast.KindEnum, "Status", true},
		{ast.KindAnnotation, "Validated", true},
	}

	if len(result.Entities) != len(wantEntities) {
		t.Fatalf("got %d entities, want %d:\n%v",
			len(result.Entities), len(wantEntities), entityNames(result.Entities))
	}

	for i, want := range wantEntities {
		got := result.Entities[i]
		if got.Kind != want.kind || got.Name != want.name || got.Exported != want.exported {
			t.Errorf("entity[%d] = {%s %q exported=%v}, want {%s %q exported=%v}",
				i, got.Kind, got.Name, got.Exported, want.kind, want.name, want.exported)
		}
	}

	// Check method receivers.
	startMethod := findEntity(result.Entities, "start")
	if startMethod == nil || startMethod.Receiver != "Server" {
		t.Errorf("start receiver = %q, want Server", safeReceiver(startMethod))
	}

	handleMethod := findEntity(result.Entities, "handle")
	if handleMethod == nil || handleMethod.Receiver != "Handler" {
		t.Errorf("handle receiver = %q, want Handler", safeReceiver(handleMethod))
	}

	// Check doc comment.
	server := findEntity(result.Entities, "Server")
	if server == nil || server.Doc == "" {
		t.Error("Server should have a doc comment")
	}
}

func assertJavaImports(t *testing.T, result *ast.ParseResult) {
	t.Helper()
	if len(result.Imports) != 2 {
		t.Fatalf("got %d imports, want 2:\n%v", len(result.Imports), result.Imports)
	}
	if result.Imports[0].Path != "java.util.List" {
		t.Errorf("import[0] = %q, want java.util.List", result.Imports[0].Path)
	}
	if result.Imports[0].Alias != "" {
		t.Errorf("import[0].Alias = %q, want empty", result.Imports[0].Alias)
	}
	if result.Imports[1].Path != "java.util.Collections.emptyList" {
		t.Errorf("import[1] = %q, want java.util.Collections.emptyList", result.Imports[1].Path)
	}
	if result.Imports[1].Alias != "static" {
		t.Errorf("import[1].Alias = %q, want static", result.Imports[1].Alias)
	}
}

func TestParseJavaFileExtension(t *testing.T) {
	if !ast.IsSupported(".java") {
		t.Fatal("IsSupported(.java) = false, want true")
	}
}

func TestIsSupported(t *testing.T) {
	supported := []string{".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java"}
	for _, ext := range supported {
		if !ast.IsSupported(ext) {
			t.Errorf("IsSupported(%q) = false, want true", ext)
		}
	}

	unsupported := []string{".rb", ".rs", ".c", ""}
	for _, ext := range unsupported {
		if ast.IsSupported(ext) {
			t.Errorf("IsSupported(%q) = true, want false", ext)
		}
	}
}

// --- helpers ---

func findEntity(entities []ast.Entity, name string) *ast.Entity {
	for i := range entities {
		if entities[i].Name == name {
			return &entities[i]
		}
	}
	return nil
}

func entityNames(entities []ast.Entity) []string {
	names := make([]string, len(entities))
	for i, e := range entities {
		names[i] = string(e.Kind) + ":" + e.Name
	}
	return names
}

func safeReceiver(e *ast.Entity) string {
	if e == nil {
		return "<nil>"
	}
	return e.Receiver
}
