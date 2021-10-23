package noteRouter

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//注解路由
//原理：通过分析目标.go文件语法树生成const变量与func/struct的映射关系，并生成对应的init函数保存映射关系到map中，在需要使用路由的地方从Map中提取对应关系即可
//使用方法：
//函数路由：import 本包后使用//#RouterMap注释保存映射关系的Map，Map类型为map[映射常量的类型]映射目标函数类型或interface{}, 映射目标使用//#Router 常量名1 常量名2 ...
//结构路由：import 本包后使用//#MappingMap 注释保存映射关系的Map, Map类型为map[映射常量的类型]interface{}, 映射目标结构使用//#Mapping 常量名1 常量名2 ....
//特别说明：因需要分析.go源文件，修改与映射关系相关定义后需要运行一次程序生成映射代码后再次编译新的映射关系才会生效
//提示：运行时的工作目录要是使用了注解路由的源文件所在目录，否则无法正常工作
type nodeType int

const (
	nodeTypeRouter nodeType = iota
	nodeTypeRouterMap
	nodeTypeMapping
	nodeTypeMappingMap
)

//注释信息
type nodeInfo struct {
	file    string          //所属文件
	position token.Position //详细位置
	pos      token.Pos   //位置
	keys     []string    //常量名
	pFunc    *funcType   //函数指针
	pStruct  *structType //结构指针
	pRouterMap     *mapType      //Router映射Map指针
	pMappingMap    *mapType      //Mapping映射Map指针
	noteType nodeType    //注释类型
}

//类型信息
type typeInfo struct {
	typeName string   //类型名称
	typeString  string  //类型描述
	constValues []string //常量定义列表
}

//Map信息
type mapType struct {
	position   token.Position //在文件中的位置
	name      string    //map名称
	pos       token.Pos //位置
	keyType   string    //map下标类型
	valueType string    //map值类型
}

//struct信息
type structType struct {
	name string    //struct名称
	pos  token.Pos //位置
	position token.Position //详细位置
}

//函数信息
type funcType struct {
	bad        bool      //是否是不受支持的函数
	funcName   string    //函数名称
	typeString string    //函数类型描述字串
	pos        token.Pos //位置
	position   token.Position //详细位置
}

//声明排序结构
type declPos struct {
	pos     token.Pos
	pMap    *mapType    //map结构，如果此位置不是map的声明，则为nil
	pNode   *nodeInfo   //注释结构，此位置是注释时保存注释结构
	pFunc   *funcType   //函数结构，此位置是函数定义时保存函数结构
	pStruct *structType //结构信息，此位置结构定义时保存结构信息
}

//按pos先后顺序排序
type linesSort []*declPos

func (up linesSort) Swap(i, j int) {
	up[i], up[j] = up[j], up[i]
}

func (up linesSort) Len() int {
	return len(up)
}

func (up linesSort) Less(i, j int) bool {
	return up[i].pos < up[j].pos
}

//记录所有声明的类型
var typeList = make([]*typeInfo, 0)

//记录所有声明的map
var mapList = make(map[string]mapType)

//记录所有声明的struct
var structList = make(map[string]structType)

//记录所有声明的全局函数
var funcList = make(map[string]funcType)

//注释列表
var nodeList = make([]nodeInfo, 0)

//声明排序
var declList = make(map[string]linesSort)

//当前处理包名
var packageName string


func parserFile(file string) error {
	fSet := token.NewFileSet()
	f, err := parser.ParseFile(fSet, file, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	declList[file] = make(linesSort,0)

	if packageName == "" {
		packageName = f.Name.Name
	}

	if packageName != f.Name.Name {
		return fmt.Errorf("处理的包名不一致，多个包引用了NoteRouter吗")
	}

	//查找注释
	for _, cms := range f.Comments {
		for _, cg := range cms.List {
			//找到RouterMap定义
			if strings.ToUpper(cg.Text) == strings.ToUpper("//#RouterMap") {
				nodeInfo := nodeInfo{
					pos:      cg.Pos(),
					position: fSet.Position(cg.Pos()),
					file:     file,
					noteType: nodeTypeRouterMap,
				}
				nodeList = append(nodeList, nodeInfo)
				//记录注释的位置
				declInfo := &declPos{
					pos:   cg.Pos(),
					pNode: &nodeInfo,
				}
				declList[file] = append(declList[file], declInfo)
				//找到映射定义
			} else if strings.HasPrefix(strings.ToUpper(cg.Text), "//#ROUTER") {
				//解析常量名称，支持多对一映射，不限制数量，#Router a b c d e
				Keys := make([]string, 0)
				b := strings.Split(cg.Text, " ")
				if len(b) >= 2 {
					Keys = b[1:]
				}
				nodeInfo := nodeInfo{
					position:  fSet.Position(cg.Pos()),
					pos:      cg.Pos(),
					noteType: nodeTypeRouter,
					keys:     Keys,
				}
				nodeList = append(nodeList, nodeInfo)
				//记录注释的位置
				declInfo := &declPos{
					pos:   cg.Pos(),
					pNode: &nodeInfo,
				}
				declList[file] = append(declList[file], declInfo)
			} else if strings.ToUpper(cg.Text) == strings.ToUpper("//#MappingMap") { //找到MappingMap定义
				nodeInfo := nodeInfo{
					position:  fSet.Position(cg.Pos()),
					pos:      cg.Pos(),
					noteType: nodeTypeMappingMap,
				}
				nodeList = append(nodeList, nodeInfo)
				//记录注释的位置
				declInfo := &declPos{
					pos:   cg.Pos(),
					pNode: &nodeInfo,
				}
				declList[file] = append(declList[file], declInfo)
			} else if strings.HasPrefix(strings.ToUpper(cg.Text), "//#MAPPING") {
				//解析常量名称，支持多对一映射，不限制数量，#Mapping a b c d e
				Keys := make([]string, 0)
				b := strings.Split(cg.Text, " ")
				if len(b) >= 2 {
					Keys = b[1:]
				}
				nodeInfo := nodeInfo{
					position:  fSet.Position(cg.Pos()),
					pos:      cg.Pos(),
					noteType: nodeTypeMapping,
					keys:     Keys,
				}
				nodeList = append(nodeList, nodeInfo)
				//记录注释的位置
				declInfo := &declPos{
					pos:   cg.Pos(),
					pNode: &nodeInfo,
				}
				declList[file] = append(declList[file], declInfo)
			}
		}
	}

	var typeST *typeInfo
	for _, n := range f.Decls {
		//声明， represents an import, constant, type or variable declaration
		gd, ok := n.(*ast.GenDecl)
		if ok {
			declInfo := declPos{
				pos:  gd.TokPos,
			}
			//记录声明的位置信息
			declList[file] = append(declList[file], &declInfo)


			for _, v := range gd.Specs {
				switch x := v.(type) {
				case *ast.TypeSpec: //类型定义，包含struct的定义
					switch t := x.Type.(type) {
					case *ast.Ident: //类型定义
						typeST = &typeInfo{
							typeName:   x.Name.Name,
							typeString: t.Name,
							constValues: make([]string,0),
						}
						//记录类型定义
						typeList = append(typeList, typeST)
					case *ast.StructType:
						structInfo := structType{
							name: x.Name.Name,
							pos:  x.Pos(),
							position: fSet.Position(x.Pos()),
						}
						//记录结构定义
						structList[structInfo.name] = structInfo
						declInfo.pStruct = &structInfo
					}
				case *ast.ValueSpec: //变量定义
					switch t := x.Type.(type) {
					case *ast.Ident: //类型定义
						if x.Names != nil && len(x.Names) > 0 {
							//记录常量声明名称
							for _, name := range x.Names {
								if typeST != nil {
									typeST.constValues = append(typeST.constValues, name.Name)
								}
							}
						}
					case *ast.MapType: //Map定义
						mapInfo := mapType{
							position:  fSet.Position(gd.Pos()),
							name:      x.Names[0].Name,
							keyType:   getTypeString(t.Key),
							valueType: getTypeString(t.Value),
							pos:       v.Pos(),
						}
						mapList[mapInfo.name] = mapInfo
						declInfo.pMap = &mapInfo
					case nil: //表达式赋值、常量定义
						if x.Values != nil {
							for _, vl := range x.Values {
								switch vn := vl.(type) {
								case *ast.CallExpr: //函数调用赋值 var xx = make(map[xx]xx)
									fp, ok := vn.Fun.(*ast.Ident)
									if ok {
										if fp.Name == "make" && vn.Args != nil && len(vn.Args) > 0 {
											mt, ok := vn.Args[0].(*ast.MapType)
											if ok {
												mapInfo := mapType{
													position:  fSet.Position(gd.Pos()),
													name:      x.Names[0].Name,
													keyType:   getTypeString(mt.Key),
													valueType: getTypeString(mt.Value),
													pos:       x.Pos(),
												}
												mapList[mapInfo.name] = mapInfo
												declInfo.pMap = &mapInfo
												continue
											}
										}
									}
								default:
									//fmt.Printf("未处理类型: %T, %v\r\n", vl, vl)
								}
							}
						}else{
							//常量定义
							if x.Names != nil {
								for _, name := range x.Names {
									if typeST != nil {
										typeST.constValues = append(typeST.constValues, name.Name)
									}
								}
							}
						}
					}
				}
			}
		} else {
			//处理函数定义
			f, ok := n.(*ast.FuncDecl)
			if ok {
				funcInfo := funcType{
					bad :  f.Recv != nil,//只接收全局函数定义，结构下的方法暂不支持
					funcName:   f.Name.Name,
					typeString: getFuncTypeString(f.Type),
					pos:        f.Pos(),
					position: fSet.Position(f.Pos()),
				}
				funcList[funcInfo.funcName] = funcInfo
				declInfo := declPos{
					pos:   f.Pos(),
					pFunc: &funcInfo,
				}
				declList[file] = append(declList[file], &declInfo)
			}
		}
	}
	return nil
}

//获取表达式类型描述字串
func getTypeString(n ast.Expr) string {
	switch x := n.(type) {
	case *ast.StarExpr:
		//递归处理指针类型
		return "*" + getTypeString(x.X)
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", x.X, x.Sel)
	case *ast.Ident:
		return fmt.Sprintf("%s", x.Name)
	case *ast.FuncType:
		return getFuncTypeString(x)
	case *ast.ArrayType:
		return "[]" + getTypeString(x.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", getTypeString(x.Key), getTypeString(x.Value))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + getTypeString(x.Elt)
	}
	return fmt.Sprintf("Unkown Type: %T, %v", n, n)
}

//获取函数类型描述字串
func getFuncTypeString(funcType *ast.FuncType) string {
	params := make([]string, 0)
	results := make([]string, 0)
	if funcType.Params != nil {
		for _, p := range funcType.Params.List {
			params = append(params, getTypeString(p.Type))
		}
	}
	if funcType.Results != nil {
		for _, r := range funcType.Results.List {
			results = append(results, getTypeString(r.Type))
		}
	}
	if len(results) == 0 && len(params) == 0 {
		return fmt.Sprintf("func()")
	}
	if len(results) == 0 {
		return fmt.Sprintf("func(%s)", strings.Join(params, ","))
	}
	if len(params) == 0 {
		return fmt.Sprintf("func()(%s)", strings.Join(results, ","))
	}

	return fmt.Sprintf("func(%s)(%s)", strings.Join(params, ","), strings.Join(results, ","))
}


func init() {
	WorkOn(".")
}

func checkConst(cType, c string) bool {
	for _, t := range typeList {
		if cType == t.typeName {
			for _, con := range t.constValues {
				if con == c {
					return true
				}
			}
			return false
		}
	}
	return false
}

//用户调用接口，可指定欲处理的源文件所在目录
func WorkOn(path string) {
	//解析源文件
	filepath.Walk(path, func(path string, info fs.FileInfo, err error) error {
		parserFile(path)
		return nil
	})

	//没有可处理的文件，不是在编译环境运行，直接返回
	if len(declList) == 0 {
		return
	}

	bRouted := false
	bMapped := false

	var routerMap *mapType
	var mappingMap *mapType

	//待处理列表
	pendingList := make([]*nodeInfo, 0)

	for _, dList := range declList {
		//定义排序
		sort.Sort(dList)
		//解析待处理列表
		end := dList.Len()
		for i, d := range dList {
			if d.pNode != nil {
				if i+1 < end {
					switch d.pNode.noteType {
					case nodeTypeMappingMap:
						if dList[i+1].pMap != nil { //找到映射map
							if mappingMap == nil {
								d.pNode.pMappingMap = dList[i+1].pMap
								pendingList = append(pendingList, d.pNode)
								mappingMap = dList[i+1].pMap
							} else {
								fmt.Printf("Warning: %s:%d #MappingMap 重复定义， 已经定义在 %s:%d 处\r\n", d.pNode.position.Filename, d.pNode.position.Line, mappingMap.position.Filename, mappingMap.position.Line)
							}
						} else {
							fmt.Printf("Warning: %s:%d #MappingMap 没有找到有效的map定义 %d\r\n", d.pNode.position.Filename, d.pNode.position.Line, dList[i+1].pos)
						}
					case nodeTypeRouterMap:
						if dList[i+1].pMap != nil {
							if routerMap == nil {
								d.pNode.pRouterMap = dList[i+1].pMap
								pendingList = append(pendingList, d.pNode)
								routerMap = dList[i+1].pMap
							} else {
								fmt.Printf("Warning: %s:%d #RouterMap 重复定义， 已经定义在 %s:%d 处\r\n", d.pNode.position.Filename, d.pNode.position.Line, routerMap.position.Filename, routerMap.position.Line)
							}
						} else {
							fmt.Printf("Warning: %s:%d #RouterMap 没有找到有效的map定义 %d\r\n", d.pNode.position.Filename, d.pNode.position.Line, dList[i+1].pos)
						}
					case nodeTypeRouter:
						if dList[i+1].pFunc != nil { //找到路由目标函数
							if dList[i+1].pFunc.bad {
								fmt.Printf("Warning: %s:%d #Router 定义的函数不是全局函数，只能接受全局函数的定义\r\n", d.pNode.position.Filename, d.pNode.position.Line)
							} else {
								d.pNode.pFunc = dList[i+1].pFunc
								pendingList = append(pendingList, d.pNode)
								bRouted = true
							}
						} else {
							fmt.Printf("Warning: %s:%d #Router 没有找到有效的函数定义\r\n", d.pNode.position.Filename, d.pNode.position.Line)
						}
					case nodeTypeMapping:
						if dList[i+1].pStruct != nil { //找到结构映射目标结构
							d.pNode.pStruct = dList[i+1].pStruct
							pendingList = append(pendingList, d.pNode)
							bMapped = true
						} else {
							fmt.Printf("Warning: %s:%d #Mapping 没有找到有效的结构定义\r\n", d.pNode.position.Filename, d.pNode.position.Line)
						}
					}
				}
			}
		}
	}
	//没有需要执行的操作
	if len(pendingList) == 0 {
		return
	}

	funcBody := "package " + packageName + "\r\n//NoteRouter自动生成文件，请不要随意修改!\r\n\r\nfunc init() {\r\n"
	//生成init代码
	if bRouted {
		if routerMap == nil {
			fmt.Println("Warning：#RouterMap 未定义，Router映射无法处理")
		}else{
			funcBody += "\t//方法映射\r\n"
			for _, node := range pendingList {
				if node.noteType == nodeTypeRouter {
					for _, c := range node.keys {
						//常量检查
						if checkConst(routerMap.keyType, c) == false {
							fmt.Printf("Warning: %s:%d 指定的常量 %s 未定义或者与映射Map的key类型 %s 不一致\r\n", node.position.Filename, node.position.Line, c, routerMap.keyType)
							continue
						}
						//函数类型检查
						if routerMap.valueType != node.pFunc.typeString && routerMap.valueType != "interface{}" && routerMap.valueType != "*interface{}" {
							fmt.Printf("Error: %s:%d 定义的函数类型 【%s】 与映射关系保存 Map【%s:%d %s】接受的值类型【%s】不一致，处理程序中断\r\n", node.pFunc.position.Filename, node.pFunc.position.Line, node.pFunc.typeString, routerMap.position.Filename,routerMap.position.Line, routerMap.name, routerMap.valueType)
							return
						}
						funcBody += fmt.Sprintf("\t%s[%s] = %s\r\n", routerMap.name, c, node.pFunc.funcName)
					}
				}
			}
			funcBody += "\t//方法映射结束\r\n"
		}
	}
	if bMapped {
		if mappingMap == nil {
			fmt.Println("Warning：#MappingMap 未定义，Mapping映射无法处理")
		}else {
			funcBody += "\r\n\t//结构映射\r\n"
			for _, node := range pendingList {
				if node.noteType == nodeTypeMapping {
					for _, c := range node.keys {
						//常量检查
						if checkConst(mappingMap.keyType, c) == false {
							fmt.Printf("Warning: %s:%d 指定的常量 %s 未定义或者与映射Map的key类型 %s 不一致\r\n", node.position.Filename, node.position.Line, c, mappingMap.keyType)
							continue
						}
						//函数类型检查
						if mappingMap.valueType != "interface{}" && mappingMap.valueType != "*interface{}" {
							fmt.Printf("Error: %s:%d 定义的结构【%s】 与映射关系保存 Map【%s:%d %s】接受的值类型【%s】不一致，处理程序中断\r\n", node.pStruct.position.Filename, node.pStruct.position.Line, node.pStruct.name, mappingMap.position.Filename, mappingMap.position.Line, mappingMap.name, mappingMap.valueType)
							return
						}
						funcBody += fmt.Sprintf("\t%s[%s] = %s{}\r\n", mappingMap.name, c, node.pStruct.name)
					}
				}
			}
			funcBody += "\t//结构映射结束\r\n"
		}
	}
	funcBody += "}\r\n"
	hashData := md5.Sum([]byte(funcBody))
	hash := hex.EncodeToString(hashData[:])
	funcBody += "//Hash:" + hash
	data, err := ioutil.ReadFile(path + "\\NodeRouterAutomation.go")
	//映射关系未发生变化，不覆写文件
	if err == nil && strings.Index(string(data), hash) != -1 {
		return
	}
	err = ioutil.WriteFile(path + "\\NodeRouterAutomation.go", []byte(funcBody), 0777)
	if err != nil {
		fmt.Printf("Error: noteRouter生成文件失败：%s\r\n", err.Error())
		return
	}
	fmt.Printf("noteRouter 生成映射文件 NodeRouterAutomation.go 成功，请重新编译以便映射生效.\r\n")
	os.Exit(0)
}