package registry

import (
	"fmt"
	"strings"
)

type labelSelectorOperator string

const (
	labelSelectorOpExists    labelSelectorOperator = "exists"
	labelSelectorOpNotExists labelSelectorOperator = "not_exists"
	labelSelectorOpEquals    labelSelectorOperator = "equals"
	labelSelectorOpNotEquals labelSelectorOperator = "not_equals"
	labelSelectorOpIn        labelSelectorOperator = "in"
	labelSelectorOpNotIn     labelSelectorOperator = "not_in"
)

const (
	LabelSelectorSyntaxSummary  = "supported selector syntax: key, !key, key=value, key!=value, key in (v1,v2), key notin (v1,v2)"
	LabelSelectorValidExample   = "valid example: selector=color=gray,version in (v2),!deprecated"
	LabelSelectorInvalidExample = "invalid example: selector=version in v2"
	LegacyLabelExample          = "legacy label example: label=color=gray"
)

// Selector 表示一组按 AND 关系组合的标签过滤条件。
//
// 当前语法是 Kubernetes 风格的一个子集，支持：
// - `key`
// - `!key`
// - `key=value`
// - `key!=value`
// - `key in (v1,v2)`
// - `key notin (v1,v2)`
//
// HTTP 调用时有两个常见接入方式：
// - 单个 `selector` 参数里用顶层逗号组合多个表达式，例如 `selector=color=gray,version in (v2),!deprecated`
// - 多个 `selector` 参数重复出现，服务端会把它们按 AND 关系合并
//
// 常见错误示例：
// - `selector=version in v2`：`in/notin` 缺少括号
// - `selector=color=`：等值匹配缺少 value
// - `selector=color=gray,,version=v2`：顶层逗号之间出现空表达式
type Selector []labelRequirement

type labelRequirement struct {
	Key      string
	Operator labelSelectorOperator
	Values   []string
}

// ParseLabelSelectorFilters 同时解析新 selector 语法和旧的 `label=key=value` 兼容参数。
func ParseLabelSelectorFilters(selectors, legacyLabels []string) (Selector, error) {
	parsed, err := parseLabelSelectors(selectors)
	if err != nil {
		return nil, withSelectorSyntaxGuide(err)
	}

	legacy, err := parseLegacyLabelFilters(legacyLabels)
	if err != nil {
		return nil, fmt.Errorf("%w; %s", err, LegacyLabelExample)
	}

	return append(parsed, legacy...), nil
}

func parseLabelSelectors(items []string) (Selector, error) {
	if len(items) == 0 {
		return nil, nil
	}

	var selector Selector
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		expressions, err := splitSelectorExpressions(item)
		if err != nil {
			return nil, err
		}
		for _, expression := range expressions {
			requirement, err := parseSelectorExpression(expression)
			if err != nil {
				return nil, err
			}
			selector = append(selector, requirement)
		}
	}
	if len(selector) == 0 {
		return nil, nil
	}

	return selector, nil
}

func parseLegacyLabelFilters(items []string) (Selector, error) {
	if len(items) == 0 {
		return nil, nil
	}

	selector := make(Selector, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid legacy label filter %q, expected key=value", item)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" || value == "" {
			return nil, fmt.Errorf("invalid legacy label filter %q, key and value are required", item)
		}

		selector = append(selector, labelRequirement{
			Key:      key,
			Operator: labelSelectorOpEquals,
			Values:   []string{value},
		})
	}
	if len(selector) == 0 {
		return nil, nil
	}

	return selector, nil
}

func splitSelectorExpressions(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	expressions := make([]string, 0, 4)
	start := 0
	depth := 0
	for index, r := range raw {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("invalid selector %q: unexpected closing parenthesis", raw)
			}
		case ',':
			if depth == 0 {
				expression := strings.TrimSpace(raw[start:index])
				if expression == "" {
					return nil, fmt.Errorf("invalid selector %q: empty requirement", raw)
				}
				expressions = append(expressions, expression)
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("invalid selector %q: unbalanced parentheses", raw)
	}

	tail := strings.TrimSpace(raw[start:])
	if tail == "" {
		return nil, fmt.Errorf("invalid selector %q: empty requirement", raw)
	}

	return append(expressions, tail), nil
}

func parseSelectorExpression(expression string) (labelRequirement, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return labelRequirement{}, fmt.Errorf("selector expression cannot be empty")
	}

	if requirement, ok, err := parseSetSelectorExpression(expression); ok || err != nil {
		return requirement, err
	}
	if idx := strings.Index(expression, "!="); idx >= 0 {
		return parseBinarySelectorExpression(expression, idx, 2, labelSelectorOpNotEquals)
	}
	if idx := strings.Index(expression, "=="); idx >= 0 {
		return parseBinarySelectorExpression(expression, idx, 2, labelSelectorOpEquals)
	}
	if idx := strings.Index(expression, "="); idx >= 0 {
		return parseBinarySelectorExpression(expression, idx, 1, labelSelectorOpEquals)
	}
	if strings.HasPrefix(expression, "!") {
		key := strings.TrimSpace(expression[1:])
		if key == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector %q: key is required", expression)
		}
		return labelRequirement{
			Key:      key,
			Operator: labelSelectorOpNotExists,
		}, nil
	}

	return labelRequirement{
		Key:      expression,
		Operator: labelSelectorOpExists,
	}, nil
}

func parseSetSelectorExpression(expression string) (labelRequirement, bool, error) {
	lower := strings.ToLower(expression)
	candidates := []struct {
		token    string
		operator labelSelectorOperator
	}{
		{token: " notin", operator: labelSelectorOpNotIn},
		{token: " in", operator: labelSelectorOpIn},
	}

	for _, candidate := range candidates {
		index := strings.Index(lower, candidate.token)
		if index < 0 {
			continue
		}

		key := strings.TrimSpace(expression[:index])
		if key == "" {
			return labelRequirement{}, true, fmt.Errorf("invalid selector %q: key is required", expression)
		}

		valuesText := strings.TrimSpace(expression[index+len(candidate.token):])
		if !strings.HasPrefix(valuesText, "(") || !strings.HasSuffix(valuesText, ")") {
			return labelRequirement{}, true, fmt.Errorf("invalid selector %q: expected values like (v1,v2)", expression)
		}

		values, err := parseSetSelectorValues(valuesText[1 : len(valuesText)-1])
		if err != nil {
			return labelRequirement{}, true, fmt.Errorf("invalid selector %q: %w", expression, err)
		}

		return labelRequirement{
			Key:      key,
			Operator: candidate.operator,
			Values:   values,
		}, true, nil
	}

	return labelRequirement{}, false, nil
}

func parseBinarySelectorExpression(expression string, index, operatorLength int, operator labelSelectorOperator) (labelRequirement, error) {
	key := strings.TrimSpace(expression[:index])
	value := strings.TrimSpace(expression[index+operatorLength:])
	if key == "" || value == "" {
		return labelRequirement{}, fmt.Errorf("invalid selector %q: key and value are required", expression)
	}

	return labelRequirement{
		Key:      key,
		Operator: operator,
		Values:   []string{value},
	}, nil
}

func parseSetSelectorValues(raw string) ([]string, error) {
	items := strings.Split(raw, ",")
	values := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item)
		if value == "" {
			return nil, fmt.Errorf("selector set values cannot be empty")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("selector set values cannot be empty")
	}

	return values, nil
}

// MatchesLabelSelector 判断实例标签是否满足 selector。
func MatchesLabelSelector(instanceLabels map[string]string, selector Selector) bool {
	if len(selector) == 0 {
		return true
	}

	for _, requirement := range selector {
		if !matchesLabelRequirement(instanceLabels, requirement) {
			return false
		}
	}

	return true
}

func matchesLabelRequirement(instanceLabels map[string]string, requirement labelRequirement) bool {
	value, exists := instanceLabels[requirement.Key]

	switch requirement.Operator {
	case labelSelectorOpExists:
		return exists
	case labelSelectorOpNotExists:
		return !exists
	case labelSelectorOpEquals:
		return exists && value == requirement.Values[0]
	case labelSelectorOpNotEquals:
		return !exists || value != requirement.Values[0]
	case labelSelectorOpIn:
		return exists && containsString(requirement.Values, value)
	case labelSelectorOpNotIn:
		return !exists || !containsString(requirement.Values, value)
	default:
		return false
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}

	return false
}

func withSelectorSyntaxGuide(err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w; %s; %s; %s", err, LabelSelectorSyntaxSummary, LabelSelectorValidExample, LabelSelectorInvalidExample)
}
