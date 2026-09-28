package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Summary struct {
	Namespace string
	Rows      []Row
	Note      string

	Pods     int
	Running  int
	Waiting  int
	Broken   int
	Done     int
	Restarts int

	CPU      float64
	Mem      float64
	HasUsage bool

	Workloads []WorkloadCount
	Quotas    []Row
	Limits    []Row

	Busiest    string
	BusiestAt  float64
	Restarted  string
	RestartsOf int
}

type WorkloadCount struct {
	Kind    Kind
	Total   int
	Pending int
}

func (c *Client) Summary(ctx context.Context, namespace string) (Summary, error) {
	out := Summary{Namespace: namespace}

	pods, err := c.podRows(ctx, namespace, KindPod)
	if err != nil {
		return out, err
	}
	out.Rows, out.Note = pods.Rows, pods.Note

	for _, row := range pods.Rows {
		out.Pods++
		out.Restarts += row.Restarts

		switch row.Severity {
		case Good:
			out.Running++
		case Warn:
			out.Waiting++
		case Bad:
			out.Broken++
		default:
			out.Done++
		}

		if row.HasCPU {
			out.HasUsage = true
			out.CPU += row.CPU
			out.Mem += row.Mem
		}
		if row.Worst > out.BusiestAt {
			out.Busiest, out.BusiestAt = row.Name, row.Worst
		}
		if row.Restarts > out.RestartsOf {
			out.Restarted, out.RestartsOf = row.Name, row.Restarts
		}
	}

	for _, kind := range KindsIn(GroupWorkloads) {
		if kind == KindPod {
			continue
		}
		items, err := c.workloads(ctx, namespace, kind)
		if err != nil || len(items) == 0 {
			continue
		}

		count := WorkloadCount{Kind: kind, Total: len(items)}
		for _, item := range items {
			if item.ready < item.desired {
				count.Pending++
			}
		}
		out.Workloads = append(out.Workloads, count)
	}

	if quotas, err := c.quotaRows(ctx, namespace); err == nil {
		out.Quotas = quotas.Rows
	}
	if limits, err := c.limitRangeRows(ctx, namespace); err == nil {
		out.Limits = limits.Rows
	}
	return out, nil
}

func (s Summary) Lines() []string {
	out := []string{
		summaryField("pods", podBreakdown(s)),
	}

	if len(s.Workloads) > 0 {
		parts := make([]string, 0, len(s.Workloads))
		for _, count := range s.Workloads {
			text := fmt.Sprintf("%d %s", count.Total, count.Kind.Spoken(count.Total))
			if count.Pending == 1 {
				text += " (one short of its replicas)"
			} else if count.Pending > 1 {
				text += fmt.Sprintf(" (%d short of their replicas)", count.Pending)
			}
			parts = append(parts, text)
		}
		out = append(out, summaryField("workloads", strings.Join(parts, ", ")))
	}

	if s.HasUsage {
		out = append(out, summaryField("usage", fmt.Sprintf("%.2f cores, %.1f GiB", s.CPU/1000, s.Mem/1024)))
	}
	if s.Pods > 0 {
		out = append(out, summaryField("restarts", fmt.Sprintf("%d in total", s.Restarts)))
	}
	if s.RestartsOf > 0 {
		out = append(out, summaryField("restarted most",
			fmt.Sprintf("%s, %s", s.Restarted, plural(s.RestartsOf, "time"))))
	}
	if s.BusiestAt > 0 {
		out = append(out, summaryField("closest to its limits",
			fmt.Sprintf("%s at %.0f%%", s.Busiest, s.BusiestAt)))
	}

	if s.Note != "" {
		out = append(out, summaryField("metrics", s.Note))
	}
	if len(s.Quotas) > 0 {
		out = append(out, summaryField("quotas", quotaSummary(s.Quotas)))
	}
	if len(s.Limits) > 0 {
		names := make([]string, 0, len(s.Limits))
		for _, row := range s.Limits {
			names = append(names, row.Name)
		}
		sort.Strings(names)
		out = append(out, summaryField("limit ranges", strings.Join(names, ", ")))
	}
	return out
}

func podBreakdown(s Summary) string {
	if s.Pods == 0 {
		return "none"
	}

	parts := make([]string, 0, 4)
	for _, part := range []struct {
		count int
		word  string
	}{
		{s.Running, "ready"},
		{s.Waiting, "not ready"},
		{s.Broken, "in trouble"},
		{s.Done, "finished"},
	} {
		if part.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.count, part.word))
		}
	}
	return fmt.Sprintf("%d (%s)", s.Pods, strings.Join(parts, ", "))
}

func quotaSummary(rows []Row) string {
	worst := rows[0]
	for _, row := range rows[1:] {
		if row.Worst > worst.Worst {
			worst = row
		}
	}
	if worst.Worst < 0 {
		return fmt.Sprintf("%s, nothing measured in percent", KindResourceQuota.count(len(rows)))
	}
	return fmt.Sprintf("%s, %s at %.0f%% of its tightest limit",
		KindResourceQuota.count(len(rows)), worst.Name, worst.Worst)
}

func (k Kind) count(n int) string {
	return fmt.Sprintf("%d %s", n, k.Spoken(n))
}

func summaryField(name, value string) string {
	return fmt.Sprintf("  %-22s %s", name, value)
}
