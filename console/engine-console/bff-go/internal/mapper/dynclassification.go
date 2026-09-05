package mapper

import (
	"context"
	"encoding/json"
	"fmt"

	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// ---------------------------------------------------------------------------
// Dynamic Classification & Management Handlers / 动态分类分级与管理处理器
// ---------------------------------------------------------------------------

// handleDynClassify 处理 /v1/dynclassification/classify
func (m *Mapper) handleDynClassify(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	fieldName := getString(v, "field_name", "")
	if fieldName == "" {
		fieldName = getString(v, "fieldName", "")
	}
	fieldValue := getString(v, "field_value", "")
	if fieldValue == "" {
		fieldValue = getString(v, "fieldValue", "")
	}
	if fieldValue == "" {
		fieldValue = getString(v, "value", "")
	}

	resp, err := client.DynClassify(ctx, &pb.DynClassificationRequest{
		FieldName:  fieldName,
		FieldValue: fieldValue,
		Domain:     getString(v, "domain", ""),
		Standard:   getString(v, "standard", ""),
	})
	if err != nil {
		return nil, err
	}

	tags := make([]map[string]any, len(resp.Tags))
	for i, t := range resp.Tags {
		tags[i] = map[string]any{
			"level":        t.Level,
			"category":     t.Category,
			"rule_id":      t.RuleId,
			"sourceEngine": t.SourceEngine,
			"domain":       t.Domain,
			"standard_id":  t.StandardId,
			"is_override":  t.IsOverride,
			"is_downgrade": t.IsDowngrade,
			"match_target": t.MatchTarget,
		}
	}

	return map[string]any{
		"tags":            tags,
		"max_level":       resp.MaxLevel,
		"audit_timestamp": resp.AuditTimestamp,
		"engine_layer":    resp.EngineLayer,
	}, nil
}

// handleDynEval 处理 /v1/dynclassification/eval
func (m *Mapper) handleDynEval(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	fieldName := getString(v, "field_name", "")
	if fieldName == "" {
		fieldName = getString(v, "fieldName", "")
	}
	if fieldName == "" {
		fieldName = getString(v, "field", "")
	}
	value := getString(v, "value", "")
	if value == "" {
		if rawVal, ok := v["value"]; ok && rawVal != nil {
			value = fmt.Sprintf("%v", rawVal)
		}
	}

	resp, err := client.DynEval(ctx, &pb.DynEvalRequest{
		FieldName: fieldName,
		Value:     value,
		Domain:    getString(v, "domain", ""),
		Standard:  getString(v, "standard", ""),
	})
	if err != nil {
		return nil, err
	}

	if resp.ResultJson != "" {
		var result map[string]any
		if err := json.Unmarshal([]byte(resp.ResultJson), &result); err == nil {
			return result, nil
		}
	}

	return map[string]any{
		"field":      resp.Field,
		"value":      resp.Value,
		"level":      resp.Level,
		"level_id":   resp.LevelId,
		"category":   resp.Category,
		"confidence": resp.Confidence,
		"matched_by": resp.MatchedBy,
	}, nil
}

// handleDynEvalRecord 处理 /v1/dynclassification/eval_record
func (m *Mapper) handleDynEvalRecord(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}

	recordMap := make(map[string]string)
	if recRaw, ok := v["record"].(map[string]any); ok {
		for k, val := range recRaw {
			recordMap[k] = fmt.Sprintf("%v", val)
		}
	}

	var recordsList []*pb.RecordEntry
	if recsRaw, ok := v["records"].([]any); ok {
		recordsList = make([]*pb.RecordEntry, len(recsRaw))
		for i, item := range recsRaw {
			rowMap := make(map[string]string)
			if row, ok := item.(map[string]any); ok {
				for k, val := range row {
					rowMap[k] = fmt.Sprintf("%v", val)
				}
			}
			recordsList[i] = &pb.RecordEntry{Fields: rowMap}
		}
	}

	resp, err := client.DynEvalRecord(ctx, &pb.DynEvalRecordRequest{
		Record:   recordMap,
		Records:  recordsList,
		Domain:   getString(v, "domain", ""),
		Standard: getString(v, "standard", ""),
	})
	if err != nil {
		return nil, err
	}

	if resp.ResultJson != "" {
		var result map[string]any
		if err := json.Unmarshal([]byte(resp.ResultJson), &result); err == nil {
			return result, nil
		}
	}

	return map[string]any{
		"level":         resp.Level,
		"overall_level": resp.OverallLevel,
	}, nil
}

// handleDynStandards 处理 /v1/dynclassification/standards
func (m *Mapper) handleDynStandards(ctx context.Context, client pb.PrivacyServiceClient, _ json.RawMessage) (any, error) {
	resp, err := client.DynStandards(ctx, &pb.DynStandardsRequest{})
	if err != nil {
		return nil, err
	}
	if resp.DetailsJson != "" {
		var details any
		if err := json.Unmarshal([]byte(resp.DetailsJson), &details); err == nil {
			return details, nil
		}
	}
	return map[string]any{
		"standards": resp.Standards,
	}, nil
}

// handleDynDomains 处理 /v1/dynclassification/domains
func (m *Mapper) handleDynDomains(ctx context.Context, client pb.PrivacyServiceClient, _ json.RawMessage) (any, error) {
	resp, err := client.DynDomains(ctx, &pb.DynDomainsRequest{})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"domains": resp.Domains,
	}, nil
}

// handleDynOperators 处理 /v1/dynclassification/operators
func (m *Mapper) handleDynOperators(ctx context.Context, client pb.PrivacyServiceClient, _ json.RawMessage) (any, error) {
	resp, err := client.DynOperators(ctx, &pb.DynOperatorsRequest{})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"operators": resp.Operators,
	}, nil
}

// handleDynValidate 处理 /v1/dynclassification/validate
func (m *Mapper) handleDynValidate(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	rulesDir := getString(v, "rules_dir", "")
	if rulesDir == "" {
		rulesDir = getString(v, "rulesDir", "")
	}

	resp, err := client.DynValidate(ctx, &pb.DynValidateRequest{
		RulesDir: rulesDir,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"is_valid": resp.IsValid,
		"valid":    resp.Valid,
		"errors":   resp.Errors,
		"warnings": resp.Warnings,
	}, nil
}

// handleDynGenerateProfile 处理 /v1/dynclassification/generate_profile
func (m *Mapper) handleDynGenerateProfile(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	docPath := getString(v, "doc_path", "")
	if docPath == "" {
		docPath = getString(v, "docPath", "")
	}

	resp, err := client.DynGenerateProfile(ctx, &pb.DynGenerateProfileRequest{
		DocPath: docPath,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":          resp.Status,
		"message":         resp.Message,
		"generated_files": resp.GeneratedFiles,
	}, nil
}
