package dynclassification

import (
	"context"
	"sync"
	"testing"
)

func TestZZRatchet(t *testing.T) {
	rules := []RuleDef{{ID: "phone", Level: LevelConfidential, Category: "pii.contact",
		FieldPatterns: []string{`(?i)(phone|mobile)`}}}
	f, err := NewClassificationFunnel(rules, NewRuleBasedNerEngine(), nil, DefaultFunnelConfig())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := f.Classify(context.Background(), "remark", "无")
	aa, _ := f.Classify(context.Background(), "remark", "无")
	t.Logf("same pointer a==b (funnel cache hit)? %v", a == aa)
	b := f.ruleEngine.Classify("remark", "无")
	c := f.ruleEngine.Classify("remark", "无")
	t.Logf("engine cache returns same pointer? %v  level=%s", b == c, b.Level)

	for i := 0; i < 7; i++ {
		f.ClearCache() // 模拟漏斗缓存淘汰：引擎缓存中的污染对象仍在
		res, _ := f.Classify(context.Background(), "remark", "无")
		t.Logf("round %d: level=%-12s conf=%.2f matched=%s ptr=%p", i, res.Level, res.Confidence, res.MatchedBy, b)
	}
	t.Logf("final engine-cached object level=%s conf=%.2f matched=%s", b.Level, b.Confidence, b.MatchedBy)
}

func TestZZRace(t *testing.T) {
	rules := []RuleDef{{ID: "phone", Level: LevelConfidential, Category: "pii.contact",
		FieldPatterns: []string{`(?i)(phone|mobile)`}}}
	f, _ := NewClassificationFunnel(rules, NewRuleBasedNerEngine(), nil, DefaultFunnelConfig())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				f.ClearCache()
				res, _ := f.Classify(context.Background(), "remark", "无")
				_ = res.Level
			}
		}()
	}
	wg.Wait()
}
