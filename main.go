package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "sql2struct",
		Usage: "Generate Go structs from SQL schema",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "sql",
				Aliases:  []string{"s"},
				Usage:    "Path to SQL schema file",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "po",
				Aliases:  []string{"p"},
				Usage:    "Name for PO struct",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "entity",
				Aliases:  []string{"e"},
				Usage:    "Name for Entity struct",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output directory",
				Value:   ".",
			},
		},
		Action: func(c *cli.Context) error {
			parser := NewSQLParser(c.String("po"), c.String("entity"))

			// 获取绝对路径
			sqlPath, err := filepath.Abs(c.String("sql"))
			if err != nil {
				return fmt.Errorf("解析SQL文件路径失败: %w", err)
			}

			if err := parser.LoadSQLFile(sqlPath); err != nil {
				return err
			}

			// 设置输出路径
			outputDir := c.String("output")
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				return fmt.Errorf("创建输出目录失败: %w", err)
			}

			// 生成代码
			if _, err := parser.GenerateStruct(outputDir); err != nil {
				return err
			}

			fmt.Printf("成功生成文件: %s\n", parser.GetOutputPath(outputDir))
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

type FieldMeta struct {
	FieldName     string
	FieldType     string
	Comment       string
	Validate      string
	OriginalField string
}

type SQLParser struct {
	TableName            string
	StructName           string
	SecondStructName     string
	Fields               []FieldMeta
	TypeMappings         map[string]string
	NullableTypeMappings map[string]string
}

func NewSQLParser(structNames ...string) *SQLParser {
	parser := &SQLParser{
		TypeMappings: map[string]string{
			"INT":       "int32",
			"SMALLINT":  "int32",
			"TINYINT":   "int32",
			"MEDIUMINT": "int32",
			"BIGINT":    "int64",
			"VARCHAR":   "string",
			"CHAR":      "string",
			"TEXT":      "string",
			"JSON":      "string",
			"DATETIME":  "datetime.DateTime",
			"DOUBLE":    "float64",
			"FLOAT":     "float32",
		},
		NullableTypeMappings: map[string]string{
			"INT":       "sql.NullInt32",
			"SMALLINT":  "sql.NullInt32",
			"TINYINT":   "sql.NullInt32",
			"MEDIUMINT": "sql.NullInt32",
			"BIGINT":    "sql.NullInt64",
			"VARCHAR":   "sql.NullString",
			"CHAR":      "sql.NullString",
			"TEXT":      "sql.NullString",
			"JSON":      "sql.NullString",
			"DATETIME":  "datetime.NullDateTime",
			"DOUBLE":    "sql.NullFloat64",
			"FLOAT":     "sql.NullFloat32",
		},
	}
	if len(structNames) > 0 {
		parser.StructName = structNames[0]
	}
	if len(structNames) > 1 {
		parser.SecondStructName = structNames[1]
	}
	return parser
}

func (p *SQLParser) LoadSQLFile(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取SQL文件失败: %w", err)
	}
	return p.Parse(string(content))
}

func (p *SQLParser) Parse(sqlContent string) error {
	tableNameRe := regexp.MustCompile(`CREATE TABLE \S+\.(\w+)(?:_\{[a-zA-Z]+\})?`)
	tableMatch := tableNameRe.FindStringSubmatch(sqlContent)
	if len(tableMatch) > 0 {
		p.TableName = tableMatch[1]
		if p.StructName == "" {
			p.StructName = ToPascalCase(p.TableName)
		}
	}

	fieldRe := regexp.MustCompile(
		"`(\\w+)`\\s+" +
			"([A-Za-z]+\\d*(\\(\\d+\\))?)\\s+" +
			"(.*?)\\s+COMMENT\\s+'(.*?)'")
	matches := fieldRe.FindAllStringSubmatch(sqlContent, -1)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		sqlType := strings.ToUpper(strings.Split(match[2], "(")[0])
		otherPart := match[4]
		comment := match[5]

		notNullRegex := regexp.MustCompile(`(?i)\bNOT\s+NULL\b`)
		hasNotNull := notNullRegex.MatchString(otherPart)

		isNullable := false
		if !hasNotNull {
			nullRegex := regexp.MustCompile(`(?i)(DEFAULT\s+NULL|NULL\b)`)
			isNullable = nullRegex.MatchString(otherPart)
		}

		var goType string
		if isNullable {
			goType = p.NullableTypeMappings[sqlType]
		} else {
			goType = p.TypeMappings[sqlType]
		}

		field := FieldMeta{
			FieldName:     ToPascalCase(match[1]),
			FieldType:     goType,
			Comment:       comment,
			OriginalField: match[1],
		}

		if strings.HasPrefix(match[2], "VARCHAR") {
			size := regexp.MustCompile(`\d+`).FindString(match[2])
			field.Validate = fmt.Sprintf("validate:\"max=%s\"", size)
		} else if strings.Contains(match[4], "加密") {
			field.Validate = "validate:\"omitempty\""
		}

		p.Fields = append(p.Fields, field)
	}
	return nil
}

func (p *SQLParser) GenerateStruct(outputDir string) (string, error) {
	fileName := filepath.Join(outputDir, ToSnakeCase(p.SecondStructName)+"_template.go")

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("package po\n\n"))
	builder.WriteString("import (\n\t\"git.woa.com/prd_base_pay_go/paycomm/datetime\"\n\t\"github.com/go-playground/validator/v10\"\n)\n\n")

	builder.WriteString(fmt.Sprintf("// %s Po结构体\n", p.StructName))
	builder.WriteString(fmt.Sprintf("type %s struct {\n", p.StructName))
	for _, field := range p.Fields {
		line := fmt.Sprintf("\t%-30s %-20s `db:\"%s\"",
			field.FieldName, field.FieldType, field.OriginalField)
		if field.Validate != "" {
			line += " " + field.Validate
		}
		line += "`" + fmt.Sprintf(" // %s", field.Comment)
		builder.WriteString(line + "\n")
	}
	builder.WriteString("}\n\n\n")

	if p.SecondStructName != "" {
		builder.WriteString(fmt.Sprintf("package entity\n\n"))
		builder.WriteString(fmt.Sprintf("//go:generate entitytool -source=$GOFILE -entity=%s \n\n", p.SecondStructName))
		builder.WriteString(fmt.Sprintf("// %s entity结构体\n", p.SecondStructName))
		builder.WriteString(fmt.Sprintf("type %s struct {\n", p.SecondStructName))

		for _, field := range p.Fields {
			privateField := strings.ToLower(field.FieldName[:1]) + field.FieldName[1:]
			fieldType := field.FieldType
			nullableToBasic := map[string]string{
				"sql.NullString":        "string",
				"sql.NullInt32":         "int32",
				"sql.NullInt64":         "int64",
				"sql.NullFloat32":       "float32",
				"sql.NullFloat64":       "float64",
				"datetime.NullDateTime": "time.Time",
			}
			if basicType, exists := nullableToBasic[fieldType]; exists {
				fieldType = basicType
			} else if fieldType == "datetime.DateTime" {
				fieldType = "time.Time"
			}
			line := fmt.Sprintf("\t%-30s %-20s // %s",
				privateField,
				fieldType,
				field.Comment)
			builder.WriteString(line + "\n")
		}
		builder.WriteString("}\n\n")

		builder.WriteString(fmt.Sprintf("func (e *%s) Validate() error {\n", p.SecondStructName))
		builder.WriteString("\treturn nil\n}\n\n")

		// 生成PO到Entity的转换方法
		builder.WriteString(fmt.Sprintf("// To%sEntity po to entity\n", p.SecondStructName))
		builder.WriteString(fmt.Sprintf("func To%sEntity(p *po.%s) (*entity.%s, error) {\n", p.SecondStructName, p.StructName, p.SecondStructName))
		builder.WriteString(fmt.Sprintf("\treturn entity.New%sBuilder().\n", p.SecondStructName))
		for _, field := range p.Fields {
			privateField := strings.ToLower(field.FieldName[:1]) + field.FieldName[1:]
			fieldAccess := field.FieldName
			switch field.FieldType {
			case "sql.NullString":
				fieldAccess += ".String"
			case "datetime.NullDateTime":
				fieldAccess += ".Time.Time()"
			case "datetime.DateTime":
				fieldAccess += ".Time()"
			case "sql.NullInt32":
				fieldAccess += ".Int32"
			case "sql.NullInt64":
				fieldAccess += ".Int64"
			case "sql.NullFloat64":
				fieldAccess += ".Float64"
			}
			builder.WriteString(fmt.Sprintf("\t\tWith%s(p.%s).\n",
				strings.Title(privateField),
				fieldAccess))
		}
		builder.WriteString("\t\tBuild()\n}\n\n")

		// 生成Entity到PO的转换方法
		builder.WriteString(fmt.Sprintf("// To%s entity to po\n", p.StructName))
		builder.WriteString(fmt.Sprintf("func To%s(e *entity.%s) (*po.%s, error) {\n",
			p.StructName, p.SecondStructName, p.StructName))
		builder.WriteString(fmt.Sprintf("\treturn &po.%s{\n", p.StructName))
		for _, field := range p.Fields {
			privateField := strings.ToLower(field.FieldName[:1]) + field.FieldName[1:]
			fieldAccess := fmt.Sprintf("e.%s()", strings.Title(privateField))
			switch field.FieldType {
			case "sql.NullString":
				fieldAccess = fmt.Sprintf("sql.NullString{String: %s, Valid: true}", fieldAccess)
			case "datetime.NullDateTime":
				fieldAccess = fmt.Sprintf("TimeToNullDateTime(%s)", fieldAccess)
			case "datetime.DateTime":
				fieldAccess = fmt.Sprintf("datetime.NewDateTime(%s)", fieldAccess)
			case "sql.NullInt32", "sql.NullInt64", "sql.NullFloat64":
				baseType := strings.TrimPrefix(field.FieldType, "sql.Null")
				fieldAccess = fmt.Sprintf("%s{%s: %s, Valid: true}",
					field.FieldType,
					strings.Title(baseType),
					fieldAccess)
			}
			builder.WriteString(fmt.Sprintf("\t\t%-15s: %s,\n",
				field.FieldName,
				fieldAccess))
		}
		builder.WriteString("\t}, nil\n}\n\n")

		needTimeFunc := false
		for _, field := range p.Fields {
			if field.FieldType == "datetime.NullDateTime" {
				needTimeFunc = true
				break
			}
		}
		if needTimeFunc {
			builder.WriteString(`// TimeToNullDateTime Time 转成 datetime.NullDateTime
func TimeToNullDateTime(t time.Time) datetime.NullDateTime {
	if !t.IsZero() {
		return datetime.NullDateTime{Time: datetime.NewDateTime(t), Valid: true}
	}
	return datetime.NullDateTime{Valid: false}
}`)
		}
	}

	if err := os.WriteFile(fileName, []byte(builder.String()), 0644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return fileName, nil
}

func (p *SQLParser) GetOutputPath(outputDir string) string {
	return filepath.Join(outputDir, ToSnakeCase(p.SecondStructName)+"_template.go")
}

func ToSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

func ToPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i := range parts {
		parts[i] = strings.Title(parts[i])
	}
	result := strings.Join(parts, "")
	if len(result) > 0 && strings.HasPrefix(result, "F") {
		remaining := result[1:]
		if remaining != "" {
			remaining = strings.ToUpper(string(remaining[0])) + remaining[1:]
		}
		return remaining
	}
	return result
}
