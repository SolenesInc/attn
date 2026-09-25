package main

import (
	_ "embed"
	"encoding/json"
	"os"
	"strings"
)

//go:embed page.html
var pageTemplate string

type pageData struct {
	GeneratedAt string               `json:"generatedAt"`
	Commit      string               `json:"commit"`
	Repo        string               `json:"repo"`
	Flows       []flowData           `json:"flows"`
	Funcs       map[string]*funcInfo `json:"funcs"`
}

func writePage(path string, page pageData) error {
	data, err := json.Marshal(page)
	if err != nil {
		return err
	}
	safe := strings.ReplaceAll(string(data), "</", `<\/`)
	return os.WriteFile(path, []byte("<!doctype html>\n"+strings.Replace(pageTemplate, "/*FLOW_DATA*/null", safe, 1)), 0o644)
}
