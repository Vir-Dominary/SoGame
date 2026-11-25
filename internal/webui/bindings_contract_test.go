package app

import (
	"reflect"
	"testing"
)

// TestWailsBindingErrorContract 是 Wails 桥接契约回归：
// 所有绑定到前端的 App 导出方法，若有两个返回值，第二返回值必须是
// error 接口本身，不能是实现了 error 的具体类型（如 *ExpressError）。
//
// 依据（Wails v2.12.0 internal/binding/boundMethod.go:101-106 +
// dispatcher/calls.go:51-59）：具体错误指针类型在成功路径返回字面 nil
// 时会以 typed-nil 形式通过 `Interface().(error)` 断言——err != nil
// 恒为真，Result 被丢弃、Err=Error()=""，前端 `if (message.error)`
// 判空 falsy 后 resolve(null)。B-1（ExpressRevealRoomCode 房间码
// 永不显示）即此陷阱的实例，本测试防止同类问题再次引入。
func TestWailsBindingErrorContract(t *testing.T) {
	// App 的绑定方法均为指针接收者，方法集在 *App 上。
	appType := reflect.TypeOf((*App)(nil))
	errorType := reflect.TypeOf((*error)(nil)).Elem()

	tested := 0
	for i := 0; i < appType.NumMethod(); i++ {
		method := appType.Method(i)
		if !method.IsExported() {
			continue
		}
		mType := method.Type
		if mType.NumOut() != 2 {
			continue
		}
		tested++
		second := mType.Out(1)
		if second != errorType {
			t.Errorf("bound method %s: second return value is %s, must be the error interface type (typed-nil trap)",
				method.Name, second.String())
		}
	}
	if tested == 0 {
		t.Fatal("no two-return-value bound methods found; contract test is not exercising anything")
	}
}
