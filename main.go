package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Method represents a method of a struct
type Method struct {
	comment string
	name    string
	iParams string
	oParams string
	body    string
}

// Field represents a field of a struct
type Field struct {
	Name string
	Type string
	// map of tags tagName:tagValue
	tags map[string]string
}

// Structure represents a struct
type Structure struct {
	packageName string
	tableName   string
	name        string
	fields      []Field
}

// Generator represents a generator
type Generator struct {
	filePath string

	header    string
	imports   []string
	structure Structure
	methods   []*Method

	data bytes.Buffer
}

var targetFile string

func main() {
	flag.Parse()
	// We accept either one directory or a list of files. Which do we have?
	args := flag.Args()
	if len(args) == 0 {
		// Default: process whole package in current directory.
		args = []string{"."}
	}
	println(args[0])

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, args[0], nil, parser.AllErrors)
	if err != nil {
		panic(err)
	}

	packageName := f.Name.Name
	err = ast.Print(fset, f)
	if err != nil {
		panic(err)
	}

	structures := make([]*Structure, 0, len(f.Decls))
	for _, v := range f.Decls {
		genDecl := v.(*ast.GenDecl)
		if genDecl.Tok == token.TYPE {
			structures = append(structures, getStructure(packageName, genDecl.Specs[0].(*ast.TypeSpec)))
		}

	}

	for _, v := range structures {
		err := Generate(v)
		if err != nil {
			panic(err)
		}
	}

}

func (g *Generator) generateDirPath() (string, error) {
	dir := filepath.Dir(g.filePath)
	if dir == "model" {
		path, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("generator: error while finding abs file path - %s", err)
		}
		path = strings.TrimSuffix(path, "")
		path = strings.ReplaceAll(path, "/", " ")
		folders := strings.Split(path, " ")
		path = ""
		for i := 0; i < len(folders)-1; i++ {
			path += folders[i]
			path += "/"
		}
		return path, nil
	}
	path, err := filepath.Abs(filepath.Dir(g.filePath))
	if err != nil {
		return "", fmt.Errorf("generator: error while finding abs file path - %s", err)
	}
	return path, nil
}

// getStructure returns structure from file
func getStructure(packageName string, typeSpec *ast.TypeSpec) *Structure {
	list := typeSpec.Type.(*ast.StructType).Fields.List
	structFields := make([]Field, len(list))
	for i, v := range list {
		var tagsArr []string
		if v.Tag != nil {
			a := v.Tag.Value
			a = strings.TrimSuffix(a, " ")
			a = strings.ReplaceAll(a, "`", "")
			a = strings.ReplaceAll(a, "\"", "")
			a = strings.ReplaceAll(a, ":", " ")
			tagsArr = strings.Split(a, " ")
		}
		tagMap := map[string]string{}
		for j := 0; j < len(tagsArr); j += 2 {
			tagMap[tagsArr[j]] = tagsArr[j+1]
		}

		indent, ok := v.Type.(*ast.Ident)
		fieldType := ""
		if ok {
			fieldType = indent.Name
		} else {
			indent := v.Type.(*ast.SelectorExpr)
			fieldType = fmt.Sprintf("%s.%s", indent.X.(*ast.Ident).Name, indent.Sel.Name)
		}
		structFields[i] = Field{
			Name: v.Names[0].Name,
			Type: fieldType,
			tags: tagMap,
		}
	}

	return &Structure{
		packageName: packageName,
		tableName:   "testTable",
		name:        typeSpec.Name.Name,
		fields:      structFields,
	}
}

// generateExec generates the exec request
func generateExec(methodName, sqlRequest, insertingValueString string) (execString string) {
	execString += "\tctg, err := p.db.Exec(ctx, " + sqlRequest + ", " + insertingValueString + ")\n"
	execString += "\tif err != nil {\n"
	execString += fmt.Sprintf("\t\treturn fmt.Errorf(\"%s error: %v \", err)\n", methodName, "%w")
	execString += "\t}\n"

	execString += "\tif ctg.RowsAffected() == 0 {\n"
	execString += fmt.Sprintf("\t\treturn fmt.Errorf(\"%s error: no rows affected\")\n", methodName)
	execString += "\t}\n\n"

	execString += "\treturn nil"

	return execString
}

// generateQueryRow generates the queryRow request
func generateQueryRow(methodName, sqlRequest, whereValue, scanString string) (queryRowString string) {
	queryRowString += "\terr = p.db.QueryRow(ctx, " + sqlRequest + ", " + whereValue + ").Scan(" + scanString + ")\n"
	queryRowString += "\tif err != nil {\n"
	queryRowString += fmt.Sprintf("\t\treturn nil, fmt.Errorf(\"%s error: %v \", err)\n", methodName, "%w")
	queryRowString += "\t}\n\n"

	queryRowString += "\treturn element, nil"

	return queryRowString
}

// generateCreate generates the create method
func generateCreate(structure Structure) *Method {
	method := Method{}

	method.name = structure.name + "Create"
	method.comment = "// " + method.name + " add new " + structure.name + " to database"
	method.iParams = "ctx context.Context, data *" + structure.packageName + "." + structure.name
	method.oParams = "err error"

	paramsString := ""
	valuesString := ""
	insertingValueString := ""
	index := 1
	for _, field := range structure.fields {
		valuesString += fmt.Sprintf("$%s, ", strconv.Itoa(index))
		paramsString += fmt.Sprintf("%s, ", field.tags["column"])
		insertingValueString += fmt.Sprintf("data.%s, ", field.Name)
		index++
	}
	valuesString = strings.TrimRight(valuesString, ", ")
	paramsString = strings.TrimRight(paramsString, ", ")
	insertingValueString = strings.TrimRight(insertingValueString, ", ")

	sqlRequest := fmt.Sprintf("\"INSERT INTO %s (%s) VALUES (%s)\"", structure.tableName, paramsString, valuesString)
	method.body += generateExec(method.name, sqlRequest, insertingValueString)

	return &method
}

// generateSelect generates select method
func generateSelect(structure Structure) *Method {
	method := Method{}

	method.name = structure.name + "Select"
	method.comment = "// " + method.name + " get " + structure.name + " from database by pk"
	method.iParams = "ctx context.Context, "
	method.oParams = "element *" + structure.packageName + "." + structure.name + ", err error"

	whereString := ""
	whereValue := ""
	paramsString := ""
	scanString := ""
	index := 1
	for _, field := range structure.fields {
		if field.tags["primary"] == "true" {
			method.iParams += fmt.Sprintf("%s %s, ", field.Name, field.Type)
			whereValue += fmt.Sprintf("%s, ", field.Name)
			whereString += fmt.Sprintf("%s = $%v AND ", field.tags["column"], index)
			index++
		}
		paramsString += field.tags["column"] + ", "
		scanString += "element." + field.Name + ", "
	}
	method.iParams = strings.TrimRight(method.iParams, ", ")
	whereString = strings.TrimRight(whereString, "AND ")
	whereValue = strings.TrimRight(whereValue, ", ")
	paramsString = strings.TrimSuffix(paramsString, ", ")
	scanString = strings.TrimSuffix(scanString, ", ")

	sqlRequest := fmt.Sprintf("\"SELECT (%s) FROM %s WHERE (%s)\"", paramsString, structure.tableName, whereString)
	method.body += generateQueryRow(method.name, sqlRequest, whereValue, scanString)

	return &method
}

// generateDelete generate delete method
func generateDelete(structure Structure) *Method {
	method := Method{}

	method.name = structure.name + "Delete"
	method.comment = "// " + method.name + " delete " + structure.name + " from database by pk"
	method.iParams = "ctx context.Context, "
	method.oParams = "err error"

	whereString := ""
	whereValue := ""
	index := 1
	for _, field := range structure.fields {
		if field.tags["primary"] == "true" {
			method.iParams += fmt.Sprintf("%s %s, ", field.Name, field.Type)
			whereValue += fmt.Sprintf("%s, ", field.Name)
			whereString += fmt.Sprintf("%s = $%v AND ", field.tags["column"], index)
			index++
		}
	}
	method.iParams = strings.TrimRight(method.iParams, ", ")
	whereString = strings.TrimRight(whereString, "AND ")
	whereValue = strings.TrimRight(whereValue, ", ")

	sqlRequest := fmt.Sprintf("\"DELETE FROM %s WHERE (%s)\"", structure.tableName, whereString)
	method.body += generateExec(method.name, sqlRequest, whereValue)

	return &method
}

// generateUpdate generate update method
func generateUpdate(structure Structure) *Method {
	method := Method{}

	method.name = structure.name + "Update"
	method.comment = "// " + method.name + " update " + structure.name + " in database by pk"
	method.iParams = "ctx context.Context, data *" + structure.packageName + "." + structure.name
	method.oParams = "err error"

	whereString := ""
	paramsString := ""
	valueString := ""
	index := 1
	for _, field := range structure.fields {
		if field.tags["primary"] == "true" {
			whereString += fmt.Sprintf("%s = $%v AND ", field.tags["column"], index)
		} else {
			paramsString += fmt.Sprintf("%s = $%v, ", field.tags["column"], index)
		}
		index++
		valueString += "data." + field.Name + ", "
	}
	whereString = strings.TrimRight(whereString, "AND ")
	paramsString = strings.TrimRight(paramsString, ", ")
	valueString = strings.TrimRight(valueString, ", ")

	sqlRequest := fmt.Sprintf("\"UPDATE %s SET (%s) WHERE (%s)\"", structure.tableName, paramsString, whereString)
	method.body += generateExec(method.name, sqlRequest, valueString)

	return &method
}

func Generate(structure *Structure) error {
	generator := Generator{
		filePath:  fmt.Sprintf("repository/%s_repository.go", structure.name),
		structure: *structure,
		data:      bytes.Buffer{},
	}

	generator.AddHeader(header)
	generator.AddImport("context")
	generator.AddImport("fmt")

	createMethod := generateCreate(*structure)
	generator.AddMethod(createMethod)

	selectMethod := generateSelect(*structure)
	generator.AddMethod(selectMethod)

	updateMethod := generateUpdate(*structure)
	generator.AddMethod(updateMethod)

	deleteMethod := generateDelete(*structure)
	generator.AddMethod(deleteMethod)

	return generator.GenerateFile()
}

const header = `// code generated automatically
// can be edited by hand if needed

// Package repository contains the repository layer of the application.
// generate this file by running: go generate repository_generator -path 'path to file holding struct' -entity 'name of struct'
package repository`

// AddHeader adds the header to the file
func (g *Generator) AddHeader(header string) {
	g.header = header
}

// AddImport adds an import to the file
func (g *Generator) AddImport(packageName string) {
	g.imports = append(g.imports, packageName)
}

// AddMethod adds a method to the file
func (g *Generator) AddMethod(method *Method) {
	g.methods = append(g.methods, method)
}

// GenerateFile generates the file
func (g *Generator) GenerateFile() error {
	// add header
	g.data.WriteString(g.header)
	g.data.WriteString("\n")
	g.data.WriteString("\n")

	// add imports
	g.data.WriteString("import (\n")
	for _, importName := range g.imports {
		g.data.WriteString("\t\"" + importName + "\"\n")
	}
	g.data.WriteString(")\n")
	g.data.WriteString("\n")

	// add interface
	g.data.WriteString("// " + g.structure.name + "Manager interface to interact with database\n")
	g.data.WriteString(fmt.Sprintf("type %sManager interface {\n", g.structure.name))
	for _, method := range g.methods {
		g.data.WriteString(fmt.Sprintf("\t%s(%s) (%s)\n", method.name, method.iParams, method.oParams))
	}
	g.data.WriteString("}\n")
	g.data.WriteString("\n")

	/*
		// add struct
		g.data.WriteString(fmt.Sprintf("type %s struct {\n", g.structure.name))
		for _, field := range g.structure.fields {
			g.data.WriteString(fmt.Sprintf("\t%s %s\n", field.Name, field.Type))
		}
		g.data.WriteString("}\n")
		g.data.WriteString("\n")
	*/
	// add methods
	for _, method := range g.methods {
		g.data.WriteString(fmt.Sprintf("%s\n", method.comment))
		g.data.WriteString(fmt.Sprintf("func (p *PostgresRepository) %s(%s) (%s){\n", method.name, method.iParams, method.oParams))
		g.data.WriteString(fmt.Sprintf("%s\n", method.body))
		g.data.WriteString("}\n\n")
	}

	return os.WriteFile(g.filePath, g.data.Bytes(), 0666)
}
