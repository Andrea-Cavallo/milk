package local

import (
	"strings"
	"testing"
)

func TestProjectMapLangs_Python(t *testing.T) {
	src := `"""docstring with class NotReal and def fake():"""

import os

MAX_DEPTH = 10

class Store:
    def __init__(self, name):
        self.name = name # def hidden():

    def get(self, key):
        return key

    async def fetch(self, url: str) -> dict:
        return {}

    class Inner:
        def ping(self):
            pass

def load(path: str, retries=3):
    def nested():
        pass
    return path
`
	syms := projectMapExtractPython([]byte(src))
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Full
	}
	for name, full := range map[string]string{
		"MAX_DEPTH":        "const MAX_DEPTH",
		"Store":            "class Store",
		"(Store).__init__": "def __init__(self, name)",
		"(Store).get":      "def get(self, key)",
		"(Store).fetch":    "async def fetch(self, url: str)",
		"load":             "def load(path: str, retries=3)",
		"nested":           "def nested()",
	} {
		if got[name] != full {
			t.Errorf("%s: got %q, want %q (all: %+v)", name, got[name], full, got)
		}
	}
	if _, ok := got["fake"]; ok {
		t.Error("comment/docstring decoys must not outline")
	}
	if strings.Contains(got["(Store).get"], "return") {
		t.Error("body leaked into signature")
	}
}

func TestProjectMapLangs_JS(t *testing.T) {
	src := `// function decoy() {}
/* class Decoy {} */
const note = "class StrDecoy { method() {} }";

export interface Shape {
  area(): number;
}

export type ID = string;

export default class Box extends Shape {
  #size = 1;

  constructor(size) {
    super();
  }

  static make(x) {
    return new Box(x);
  }

  area(): number {
    return this.#size;
  }
}

export function pack(items) {
  return items.length;
}

export const unpack = (items) => items;

const anon = function () {};
`
	syms := projectMapExtractJS([]byte(src))
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Full
	}
	for name := range map[string]bool{
		"Shape": true, "ID": true, "Box": true, "pack": true, "unpack": true, "anon": true,
		"(Box).constructor": true, "(Box).make": true, "(Box).area": true,
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing %q (all: %+v)", name, got)
		}
	}
	for name := range map[string]bool{"decoy": true, "Decoy": true, "StrDecoy": true} {
		if _, ok := got[name]; ok {
			t.Errorf("decoy %q must not outline", name)
		}
	}
	if strings.Contains(got["(Box).area"], "return") {
		t.Error("body leaked into signature")
	}
	if !strings.HasPrefix(got["ID"], "type ID") {
		t.Errorf("ID full = %q", got["ID"])
	}
}

func TestProjectMapLangs_Java(t *testing.T) {
	src := `package com.x;

import java.util.List;

public class Store {
    private String name; // field: skipped

    public Store(String name) {
        this.name = name;
    }

    public String get(String key) {
        return key; // if (x) { not a method
    }

    static void reset() {
    }

    private interface Hook {
        void onEvent(String e);
    }
}
`
	syms := projectMapExtractJava([]byte(src))
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Full
	}
	for name := range map[string]bool{
		"Store": true, "(Store).Store": true, "(Store).get": true, "(Store).reset": true,
		"Hook": true, "(Hook).onEvent": true,
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing %q (all: %+v)", name, got)
		}
	}
	if _, ok := got["name"]; ok {
		t.Error("fields must not outline")
	}
}

func TestProjectMapLangs_Rust(t *testing.T) {
	src := `// fn decoy() {}
use std::fmt;

pub struct Store {
    name: String,
}

pub const MAX: usize = 8;

trait Sink {
    fn push(&mut self, b: u8);
}

impl Store {
    pub fn new(name: String) -> Self {
        Self { name } // fn hidden() {}
    }

    fn get(&self) -> &str {
        &self.name
    }
}

impl fmt::Display for Store {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        write!(f, "{}", self.name)
    }
}

fn main() {}
`
	syms := projectMapExtractRust([]byte(src))
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Full
	}
	for name := range map[string]bool{
		"Store": true, "MAX": true, "Sink": true, "impl Store": true,
		"(Store).new": true, "(Store).get": true, "(Store).fmt": true, "main": true,
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing %q (all: %+v)", name, got)
		}
	}
	if _, ok := got["decoy"]; ok {
		t.Error("comment decoy must not outline")
	}
	if _, ok := got["hidden"]; ok {
		t.Error("nested fn must not outline")
	}
	if strings.Contains(got["(Store).get"], "&self.name") {
		t.Error("body leaked into signature")
	}
}
