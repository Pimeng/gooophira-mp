package config

import (
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// FileWatcher 通过轮询文件的修改时间与大小来侦测配置文件变更，变更时回调 onChange。
//
// 不依赖 fsnotify 等外部库（保持零额外依赖、跨平台一致）。轮询天然带去抖：两次轮询间的
// 多次写入只触发一次回调。对应 TS core/configWatcher.ts（其用 fs.watch + 200ms 去抖）。
type FileWatcher struct {
	paths    []string
	interval time.Duration
	onChange func()
	stop     chan struct{}
	done     chan struct{}
}

// fileSig 是用于比较的文件指纹（不存在时三者皆零值）。
type fileSig struct {
	exists  bool
	modNano int64
	size    int64
}

func statSig(path string) fileSig {
	fi, err := os.Stat(path)
	if err != nil {
		return fileSig{}
	}
	return fileSig{exists: true, modNano: fi.ModTime().UnixNano(), size: fi.Size()}
}

// NewFileWatcher 创建一个轮询配置文件的监视器（尚未启动）。interval<=0 时回退 2s。
func NewFileWatcher(path string, interval time.Duration, onChange func()) *FileWatcher {
	return newFileWatcher([]string{path}, interval, onChange)
}

// NewConfigDirWatcher watches the fixed multi-file configuration set. It also
// observes absent optional files so creating one triggers a reload.
func NewConfigDirWatcher(dir string, interval time.Duration, onChange func()) *FileWatcher {
	paths := make([]string, 0, len(ConfigFileNames()))
	for _, name := range ConfigFileNames() {
		paths = append(paths, filepath.Join(dir, name))
	}
	return newFileWatcher(paths, interval, onChange)
}

func newFileWatcher(paths []string, interval time.Duration, onChange func()) *FileWatcher {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &FileWatcher{
		paths:    paths,
		interval: interval,
		onChange: onChange,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start 启动后台轮询（非阻塞）。以启动时的文件指纹为基线，故启动本身不触发回调。
func (w *FileWatcher) Start() {
	go w.loop()
}

func (w *FileWatcher) loop() {
	defer close(w.done)
	last := w.signatures()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			cur := w.signatures()
			if !reflect.DeepEqual(cur, last) {
				last = cur
				w.onChange()
			}
		}
	}
}

func (w *FileWatcher) signatures() []fileSig {
	out := make([]fileSig, len(w.paths))
	for i, path := range w.paths {
		out[i] = statSig(path)
	}
	return out
}

// Stop 停止轮询并等待后台 goroutine 退出（幂等）。
func (w *FileWatcher) Stop() {
	select {
	case <-w.stop:
		// 已停止
	default:
		close(w.stop)
	}
	<-w.done
}
