package notify

import "github.com/gen2brain/beeep"

type Notifier interface {
	Notify(title, body string) error
}

type beeepNotifier struct{}

func (beeepNotifier) Notify(title, body string) error {
	// icon path 可留空；不同平台自行處理圖示
	return beeep.Notify(title, body, "")
}

func New() Notifier {
	return beeepNotifier{}
}
