//go:build !kylin

package main

import "testing"

func TestQuestionButtonLabelsMatchWailsWindowsResults(t *testing.T) {
	primary, secondary, cancel := questionButtonLabels(
		"windows", "隐藏主窗", "直接退出", "取消",
	)
	if primary != "Yes" || secondary != "No" || cancel != "Cancel" {
		t.Fatalf("windows labels = %q/%q/%q", primary, secondary, cancel)
	}
}

func TestQuestionButtonLabelsKeepCustomLabelsOffWindows(t *testing.T) {
	primary, secondary, cancel := questionButtonLabels(
		"darwin", "隐藏主窗", "直接退出", "取消",
	)
	if primary != "隐藏主窗" || secondary != "直接退出" || cancel != "取消" {
		t.Fatalf("darwin labels = %q/%q/%q", primary, secondary, cancel)
	}
}
