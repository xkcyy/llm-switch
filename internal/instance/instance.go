package instance

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Acquire 获取单实例互斥体。already 为 true 表示已有实例在运行。
func Acquire() (release func(), already bool, err error) {
	name, err := windows.UTF16PtrFromString(`Local\LLM-Switch-SingleInstance`)
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		var errno windows.Errno
		if errors.As(err, &errno) && errno == windows.ERROR_ALREADY_EXISTS {
			return func() {}, true, nil
		}
		return nil, false, err
	}
	return func() { windows.CloseHandle(h) }, false, nil
}
