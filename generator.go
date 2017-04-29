package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"reflect"
	"strings"

	"github.com/dave/jennifer/jen"
)

// generator will work on the selected structure of one file
type generator struct {
	defs       map[*ast.Ident]types.Object
	pkg        *types.Package
	fs         *token.FileSet
	files      []*ast.File
	structures []*ast.TypeSpec
	buf        *jen.File
	pkgName    string
	dirName    string
	inited     bool
	w          io.Writer
}

func newGenerator(str []*ast.TypeSpec, dirname string, files []*ast.File, fs *token.FileSet, pkgName string, w io.Writer) *generator {
	return &generator{
		structures: str,
		pkgName:    pkgName,
		w:          w,
		dirName:    dirname,
		fs:         fs,
		files:      files,
	}
}

func (g *generator) init() error {

	/**
	initializing package parsing with the go/type
	*/

	g.defs = make(map[*ast.Ident]types.Object)
	infos := &types.Info{
		Defs: g.defs,
	}

	config := types.Config{Importer: importer.Default(), FakeImportC: true}

	var err error
	g.pkg, err = config.Check(g.dirName, g.fs, g.files, infos)
	if err != nil {
		return err
	}

	_, err = g.w.Write([]byte("// Code generated by github.com/mrsinham/zerogen. DO NOT EDIT.\n"))
	if err != nil {
		return err
	}
	g.buf = jen.NewFilePathName(g.pkg.Path(), g.pkgName)
	g.inited = true
	return nil
}

func (g *generator) do() error {

	var err error
	if !g.inited {
		err = g.init()
		if err != nil {
			return err
		}
	}
	for i := range g.structures {
		switch g.structures[i].Type.(type) {
		case *ast.StructType:
			if g.defs[g.structures[i].Name] != nil {
				curType := g.defs[g.structures[i].Name].Type()
				at := strings.Split(curType.String(), ".")

				if len(at) == 0 {
					return nil
				}
				objectID := at[len(at)-1]
				idObjectID := strings.ToLower(objectID[:1])

				var magicalCode []jen.Code
				magicalCode, _, err = g.doOne(g.defs[g.structures[i].Name], g.defs[g.structures[i].Name], nil)
				if err != nil {
					return err
				}

				g.buf.Func().Params(jen.Id(idObjectID).Op("*").Id(objectID)).Id("Reset").Params().Block(
					// here generate the code
					magicalCode...,
				)
			}
		default:
		}

	}

	return g.buf.Render(g.w)
}

func (g *generator) doOne(t types.Object, parent types.Object, fieldHierarcy []string) (magicalCode []jen.Code, hasUnexportedField bool, err error) {

	var st *types.Struct
	var ok bool
	if st, ok = t.Type().Underlying().(*types.Struct); !ok {
		err = errors.New("type spec is not a structtype")
		return
	}

	at := strings.Split(parent.Type().String(), ".")

	if len(at) == 0 {
		return
	}
	objectID := at[len(at)-1]
	idObjectID := strings.ToLower(objectID[:1])

	for i := 0; i < st.NumFields(); i++ {

		f := st.Field(i)

		//ms := types.NewMethodSet(f.Type())
		//spew.Dump(ms)
		// son and unexported field
		if t.String() != parent.String() && !f.Exported() {
			hasUnexportedField = true
		}

		newHierarchy := append(fieldHierarcy, f.Name())

		var nonil bool
		// read the current tags

		if st.Tag(i) != "" {
			bst := reflect.StructTag(strings.Trim(st.Tag(i), "`"))
			var tc string
			if tc = bst.Get("zerogen"); tc == "nonil" {
				nonil = true
			}
		}

		// fieds without names
		if f.Anonymous() {

			switch f.Type().Underlying().(type) {
			case *types.Interface:
				magicalCode = append(magicalCode, jen.Id(idObjectID).Op(".").Id(f.Name()).Op("=").Nil())
			case *types.Struct:

				// recursive way
				var mc []jen.Code
				var unexported bool
				mc, unexported, err = g.doOne(f, parent, newHierarchy)
				if err != nil {
					return
				}

				if unexported && !samePackage(f, t) {

					value := jen.Id(idObjectID)
					for i := range newHierarchy {
						value.Op(".").Id(newHierarchy[i])
					}
					value.Op("=")
					err = writeType(f.Type(), nonil, value)
					if err != nil {
						return
					}
					magicalCode = append(magicalCode, value)
				} else {
					magicalCode = append(magicalCode, mc...)
				}
			default:
				err = fmt.Errorf("anonymous field of type %q not handled yet", t.String())
				return
			}
			continue
		}

		value := jen.Id(idObjectID).Op(".").Id(f.Name()).Op("=")
		err = writeType(f.Type(), nonil, value)
		if err != nil {
			return
		}

		magicalCode = append(magicalCode, value)

	}

	return
}

func writeType(typ types.Type, nonil bool, value *jen.Statement) error {
	switch t := typ.Underlying().(type) {
	case *types.Basic:
		bi := t.Info()
		if bi&types.IsInteger != 0 {
			value.Lit(0)
		}
		if bi&types.IsString != 0 {
			value.Lit("")
		}
	case *types.Array:
		v, err := write(t)
		if err != nil {
			return err
		}

		value.Add(jen.List(v)).Block()
	case *types.Map:
		if nonil {
			v, err := write(t)
			if err != nil {
				return err
			}
			value.Make(v)
		} else {
			value.Nil()
		}
	case *types.Pointer:
		if nonil {
			// we want to know how to write the underlying object
			v, err := write(t.Elem())
			if err != nil {
				return err
			}
			// instantiate new pointer
			value.Op("&").Add(v).Block()
		} else {
			value.Nil()
		}
	case *types.Slice:
		if nonil {
			v, err := write(t)
			if err != nil {
				return err
			}
			value.Make(jen.List(v, jen.Lit(0)))
		} else {
			value.Nil()
		}
	case *types.Chan:
		if nonil {
			v, err := write(t)
			if err != nil {
				return err
			}
			value.Make(v)
		} else {
			value.Nil()
		}

	case *types.Signature:
		value.Nil()
	case *types.Interface:
		value.Nil()
	case *types.Struct:
		v, err := write(typ)
		if err != nil {
			return err
		}
		value.Add(v).Block()
	default:
		return errors.New("unsupported type")
	}
	return nil
}

func write(typ types.Type) (*jen.Statement, error) {
	switch t := typ.(type) {
	case *types.Basic:
		if o := strings.LastIndex(t.String(), "."); o >= 0 {
			return jen.Qual(t.String()[:o], t.String()[o+1:]), nil
		}
		// op is not the right method, it should be a
		return jen.Id(t.String()), nil
	case *types.Named:
		if o := strings.LastIndex(t.String(), "."); o >= 0 {
			return jen.Qual(t.String()[:o], t.String()[o+1:]), nil
		}
		return jen.Lit(t.String()), nil
	case *types.Map:
		key, err := write(t.Key())
		if err != nil {
			return nil, err
		}
		var val *jen.Statement
		val, err = write(t.Elem())
		if err != nil {
			return nil, err
		}
		return jen.Map(key).Add(val), nil

	case *types.Array:
		j := jen.Index(jen.Lit(int(t.Len())))
		el, err := write(t.Elem())
		if err != nil {
			return nil, err
		}
		j.Add(el)
		return j, nil
	case *types.Slice:
		j := jen.Index()
		el, err := write(t.Elem())
		if err != nil {
			return nil, err
		}
		j.Add(el)
		return j, nil
	case *types.Pointer:
		// remove the pointer star
		id := t.String()[1:]
		if o := strings.LastIndex(id, "."); o >= 0 {
			return jen.Op("*").Qual(id[:o], id[o+1:]), nil
		}
		return jen.Op("*").Lit(id), nil
	case *types.Chan:
		el, err := write(t.Elem())
		if err != nil {
			return nil, err
		}
		return jen.Chan().Add(el), nil
	case *types.Signature:
		id := t.String()
		if o := strings.LastIndex(id, "."); o >= 0 {
			return jen.Qual(id[:o], id[o+1:]), nil
		}
		return jen.Op(id), nil
	case *types.Struct:
		if o := strings.LastIndex(t.String(), "."); o >= 0 {
			return jen.Qual(t.String()[:o], t.String()[o+1:]), nil
		}
		// op is not the right method, it should be a
		return jen.Id(t.String()), nil
	default:
		return nil, fmt.Errorf("unsupported type %v", t.String())
	}
}

func samePackage(t types.Object, t2 types.Object) bool {
	return t.Pkg().Path() == t2.Pkg().Path()
}
