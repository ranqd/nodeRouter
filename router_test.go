package noteRouter

import (
	"fmt"
	"reflect"
	"testing"
)

type ConstType int

const(
	Const0  ConstType = iota
	Const1
	Const2
	Const3
)

const(
	AA = 1
)
//#RouterMap

var m = make(map[ConstType]func())

//#MappingMap
var mm = make(map[ConstType]interface{})

func TestParser(t *testing.T) {
	m[Const1]()
	m[Const2]()
	m[Const3]()
	fmt.Printf("The struct is %s\r\n", reflect.TypeOf(mm[Const1]).Name())
}

func UnaryExpr(t testing.T, i map[string]interface{}, a ...interface{}) (int, error) {
	return 0, nil
}

//#Router Const1
func f1() {
	fmt.Println("Const1 -> f1")
}

//#Router Const2 Const3
func f2() {
	fmt.Println("Const2 Const3 -> f2")
}

//#Mapping Const1
type SSS struct {

}
