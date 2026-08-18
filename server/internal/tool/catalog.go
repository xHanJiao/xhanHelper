package tool

import (
	"bytes"
	"fmt"
	"strings"
)

// ToolCatalog 生成工具组目录（组名 + 描述 + 全部函数名，不含参数 schema），
// 供渐进式工具暴露的初始提示词使用。输出按工具名排序，字节稳定（前缀缓存友好）。
// expose_tools 与 hidden 工具不进入目录。
// 函数名全量透出（LLM 需按函数名调用 expose_tools.enable）；组级描述由工具自身
// Catalog() 提供（CatalogProvider），未实现该接口的工具回退到 Description()。
func (r *Registry) ToolCatalog() string {
	var buf bytes.Buffer
	buf.WriteString("可用工具组（函数完整定义按需加载）。需要调用某组函数时，先调用 expose_tools.enable([{name:\"组名\", functions:[\"函数名\"]}])，一次可启用多个组的多个函数，启用后下一轮即可直接调用。目录中的函数名可直接调用（参数简单时）；若参数结构复杂或不确定，先按需启用以获取完整参数定义。\n\n")
	for _, name := range r.sortedVisibleToolNames() {
		if name == "expose_tools" {
			continue
		}
		t := r.tools[name]
		entry := CatalogEntry{ShortDesc: t.Description(), Funcs: functionNames(t.Functions())}
		if provider, ok := t.(CatalogProvider); ok {
			if e, ok2 := provider.Catalog(); ok2 {
				entry = e
			}
		}
		buf.WriteString(fmt.Sprintf("- %s: %s\n", name, entry.ShortDesc))
		allNames := functionNames(t.Functions())
		if len(allNames) > 0 {
			buf.WriteString(fmt.Sprintf("  函数: %s\n", strings.Join(allNames, ", ")))
		}
		if entry.Others != "" {
			buf.WriteString(fmt.Sprintf("  另有: %s\n", entry.Others))
		}
	}
	return buf.String()
}

// ValidateCatalog 校验所有可见（非 hidden、非 expose_tools）工具：
// 1) 必须实现 CatalogProvider（否则 LLM 目录里看不到其定制信息，新增工具忘写即 panic）
// 2) ShortDesc 非空；Funcs 中每个函数名必须存在于 Functions()（防拼写错误）。
// 在 Register 完成后调用。
func (r *Registry) ValidateCatalog() {
	for name, t := range r.tools {
		if name == "expose_tools" || r.hiddenNames[name] {
			continue
		}
		provider, ok := t.(CatalogProvider)
		if !ok {
			panic(fmt.Sprintf("[tool] tool %q must implement CatalogProvider (tool catalog requires custom short description)", name))
		}
		entry, _ := provider.Catalog()
		if strings.TrimSpace(entry.ShortDesc) == "" {
			panic(fmt.Sprintf("[tool] tool %q Catalog().ShortDesc is empty", name))
		}
		defined := make(map[string]bool)
		for _, fn := range t.Functions() {
			defined[fn.Name] = true
		}
		for _, fnName := range entry.Funcs {
			if !defined[fnName] {
				panic(fmt.Sprintf("[tool] tool %q Catalog().Funcs references undefined function: %s (must match Functions())", name, fnName))
			}
		}
	}
}

func functionNames(funcs []FuncSchema) []string {
	names := make([]string, len(funcs))
	for i, f := range funcs {
		names[i] = f.Name
	}
	return names
}
