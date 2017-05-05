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

	g.buf = jen.NewFilePathName(g.pkg.Path(), g.pkgName)
	g.buf.PackageComment("Code generated by github.com/mrsinham/zerogen. DO NOT EDIT.")
	g.inited = true
	return nil
}

func (g *generator) do() error {

	if !g.inited {
		if err := g.init(); err != nil {
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

				var err error
				g.buf.Func().Params(
					jen.Id(idObjectID).Op("*").Id(objectID),
				).Id("Reset").Params().BlockFunc(func(grp *jen.Group) {
					err = g.doOne(
						grp,
						g.defs[g.structures[i].Name],
						g.defs[g.structures[i].Name],
						nil,
					)
				})
				if err != nil {
					return err
				}
			}
		default:
		}

	}

	return g.buf.Render(g.w)
}

func (g *generator) getStruct(t types.Object, parent types.Object) (*types.Struct, []string, error) {
	var st *types.Struct
	var ok bool
	if st, ok = t.Type().Underlying().(*types.Struct); !ok {
		return nil, nil, errors.New("type spec is not a structtype")
	}

	at := strings.Split(parent.Type().String(), ".")

	if len(at) == 0 {
		return nil, nil, nil
	}

	return st, at, nil
}

func (g *generator) hasUnexportedField(t types.Object, parent types.Object) (bool, error) {
	st, _, err := g.getStruct(t, parent)
	if err != nil {
		return false, err
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if t.String() != parent.String() && !f.Exported() {
			return true, nil
		}
	}
	return false, nil
}

func (g *generator) doOne(grp *jen.Group, t types.Object, parent types.Object, fieldHierarcy []string) error {

	st, at, err := g.getStruct(t, parent)
	if err != nil {
		return err
	}

	objectID := at[len(at)-1]
	idObjectID := strings.ToLower(objectID[:1])

	for i := 0; i < st.NumFields(); i++ {

		f := st.Field(i)

		newHierarchy := append(fieldHierarcy, f.Name())

		var nonil bool
		// read the current tags

		if st.Tag(i) != "" {
			bst := reflect.StructTag(strings.Trim(st.Tag(i), "`"))
			var tc string
			if tc = bst.Get("zg"); tc == "nonil" {
				nonil = true
			}
		}

		// fields with names
		if !f.Anonymous() {
			var err error
			grp.Id(idObjectID).Dot(f.Name()).Op("=").Do(func(s *jen.Statement) {
				err = writeType(s, f.Type(), nonil)
			})
			if err != nil {
				return err
			}
			continue
		}

		// fields without names
		switch f.Type().Underlying().(type) {
		case *types.Interface:
			setNull := jen.Id(idObjectID).Dot(f.Name()).Op("=").Nil()
			if hasResetMethod(f) {
				call := jen.Id(idObjectID)
				for i := range newHierarchy {
					call.Dot(newHierarchy[i])
				}

				grp.If(call.Clone().Op("!=").Nil()).Block(
					call.Clone().Dot("Reset").Call(),
				).Else().Block(
					setNull,
				)
			} else {
				grp.Add(setNull)
			}
		case *types.Struct:

			if hasResetMethod(f) {
				// reset method, call it
				grp.Id(idObjectID).Do(func(s *jen.Statement) {
					for i := range newHierarchy {
						s.Dot(newHierarchy[i])
					}
				}).Dot("Reset").Call()
				continue
			}

			unexported, err := g.hasUnexportedField(f, parent)
			if err != nil {
				return err
			}

			if unexported && !samePackage(f, t) {
				var err error
				grp.Id(idObjectID).Do(func(s *jen.Statement) {
					for i := range newHierarchy {
						s.Dot(newHierarchy[i])
					}
				}).Op("=").Do(func(s *jen.Statement) {
					err = writeType(s, f.Type(), nonil)
				})
				if err != nil {
					return err
				}
			} else {
				if err := g.doOne(grp, f, parent, newHierarchy); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("anonymous field of type %q not handled yet", t.String())
		}

	}

	return nil
}

func writeType(value *jen.Statement, typ types.Type, nonil bool) error {
	switch t := typ.Underlying().(type) {
	case *types.Basic:
		bi := t.Info()
		if bi&types.IsInteger != 0 {
			value.Lit(0)
		}
		if bi&types.IsString != 0 {
			value.Lit("")
		}
		if bi&types.IsFloat != 0 {
			value.Lit(0.0)
		}
		if bi&types.IsComplex != 0 {
			value.Lit(0)
		}

	case *types.Array:
		v, err := write(t)
		if err != nil {
			return err
		}

		value.Add(v).Block()
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
			value.Make(v, jen.Lit(0))
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
		return jen.Id(id), nil
	case *types.Struct:
		id := t.String()
		if o := strings.LastIndex(id, "."); o >= 0 {
			return jen.Qual(id[:o], id[o+1:]), nil
		}
		// op is not the right method, it should be a
		return jen.Id(id), nil
	default:
		return nil, fmt.Errorf("unsupported type %v", t.String())
	}
}

func samePackage(t types.Object, t2 types.Object) bool {
	return packageFromType(t.Type()) == packageFromType(t2.Type())
}

func packageFromType(t types.Type) string {
	id := t.String()
	o := strings.LastIndex(id, ".")
	if o < 0 {
		return ""
	}
	return id[:o]
}

func hasResetMethod(o types.Object) bool {

	a := []types.Type{o.Type(), types.NewPointer(o.Type())}
	for i := range a {
		ms := types.NewMethodSet(a[i])
		for j := 0; j < ms.Len(); j++ {
			object := ms.At(j).Obj()
			if m, ok := object.(*types.Func); ok {

				if strings.Contains(m.String(), "Reset()") {
					return true
				}
			}
		}
	}
	return false
}
