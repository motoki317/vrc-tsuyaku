package asr

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"

	ort "github.com/getcharzp/onnxruntime_purego"
	"github.com/motoki317/vrc-tsuyaku/internal/catalog"
)

// Recognizer consumes one phrase of 16 kHz mono float32 audio.
// Transcribe rejects empty audio and non-finite samples. Calls can run
// concurrently. No state carries over between calls.
// Context cancellation takes effect between inference calls, so an active ONNX
// call can delay it.
type Recognizer interface {
	Transcribe(context.Context, []float32) (string, error)
}

var processEngine struct {
	sync.Mutex
	active  *Engine
	library string
}

// RuntimeResident reports whether the binding still holds the runtime library.
func RuntimeResident() bool {
	processEngine.Lock()
	defer processEngine.Unlock()
	return processEngine.library != ""
}

// Engine owns the process's ORT environment and all loaded model sessions.
// Sessions run on the CPU: recognizers use four intra-op threads, Silero uses one.
type Engine struct {
	mu        sync.RWMutex
	loads     sync.Mutex
	ort       *ort.Engine
	models    map[catalog.HaasFiles]*cachedModel[*Haas]
	parakeets map[catalog.ParakeetFiles]*cachedModel[*Parakeet]
	sileros   map[string]*cachedModel[*Silero]
}

type cachedModel[T interface{ close() }] struct {
	model T
	users int
}

// NewEngine loads ORT from libraryPath, which must be non-empty.
// Only one Engine can be open at a time. The binding,
// getcharzp/onnxruntime_purego, keeps the library loaded
// after Close. After Close, NewEngine can open the same library again. Changing
// libraries requires a new process.
func NewEngine(libraryPath string) (*Engine, error) {
	processEngine.Lock()
	defer processEngine.Unlock()
	if processEngine.active != nil {
		return nil, fmt.Errorf("an ORT engine is already open: share it between recognizers")
	}
	if libraryPath == "" {
		return nil, fmt.Errorf("ORT library path is empty")
	}
	libraryPath, err := filepath.Abs(libraryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ORT library: %w", err)
	}
	libraryPath, err = filepath.EvalSymlinks(libraryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve ORT library: %w", err)
	}
	if processEngine.library != "" && processEngine.library != libraryPath {
		return nil, fmt.Errorf("ORT library %q is already resident: restart the process to load %q", processEngine.library, libraryPath)
	}
	runtime, err := ort.NewEngine(libraryPath)
	if err != nil {
		return nil, fmt.Errorf("load ORT %q: %w", libraryPath, err)
	}
	e := &Engine{
		ort: runtime, models: make(map[catalog.HaasFiles]*cachedModel[*Haas]),
		parakeets: make(map[catalog.ParakeetFiles]*cachedModel[*Parakeet]), sileros: make(map[string]*cachedModel[*Silero]),
	}
	processEngine.active = e
	processEngine.library = libraryPath
	return e, nil
}

// Close waits for model loads, recognition and VAD calls, then unloads all models
// and closes the ORT environment.
func (e *Engine) Close() {
	processEngine.Lock()
	defer processEngine.Unlock()
	e.loads.Lock()
	defer e.loads.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ort == nil {
		return
	}
	closeModels(e.models)
	closeModels(e.parakeets)
	closeModels(e.sileros)
	e.ort.Close()
	e.ort = nil
	processEngine.active = nil
	releaseNativeMemory()
}

// LoadHaas loads four sessions and a vocabulary from five file paths.
// It converts paths to absolute paths and uses them as the cache key.
// Reusing the same cache key returns the cached model.
// Release the returned reference after its last inference call. The release
// function is idempotent. The last release unloads the model.
// Loading both values of catalog.HaasBundle's int8Encoder argument loads the
// features, decoder and joint sessions twice, once for each encoder.
func (e *Engine) LoadHaas(files catalog.HaasFiles) (*Haas, func(), error) {
	var err error
	for _, path := range []*string{&files.Features, &files.Encoder, &files.Decoder, &files.Joint, &files.Vocabulary} {
		if *path == "" {
			return nil, nil, fmt.Errorf("Haas model path is empty")
		}
		*path, err = filepath.Abs(*path)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve Haas path: %w", err)
		}
	}
	return loadModel(e, e.models, files, func() (*Haas, error) { return newHaas(e, files) })
}

// LoadParakeet loads Parakeet TDT 0.6B v2 or v3 int8 from four file paths.
// It converts paths to absolute paths and uses them as the cache key.
// Reusing the same cache key returns the cached model.
// Release the returned reference after its last inference call. The release
// function is idempotent. The last release unloads the model.
func (e *Engine) LoadParakeet(files catalog.ParakeetFiles) (*Parakeet, func(), error) {
	var err error
	for _, path := range []*string{&files.Features, &files.Encoder, &files.DecoderJoint, &files.Vocabulary} {
		if *path == "" {
			return nil, nil, fmt.Errorf("Parakeet model path is empty")
		}
		*path, err = filepath.Abs(*path)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve Parakeet path: %w", err)
		}
	}
	return loadModel(e, e.parakeets, files, func() (*Parakeet, error) { return newParakeet(e, files) })
}

func loadModel[K comparable, T interface{ close() }](e *Engine, cache map[K]*cachedModel[T], key K, load func() (T, error)) (T, func(), error) {
	if err := e.beginLoad(); err != nil {
		var zero T
		return zero, nil, err
	}
	defer e.endLoad()
	entry := cache[key]
	if entry == nil {
		model, err := load()
		if err != nil {
			return model, nil, err
		}
		entry = &cachedModel[T]{model: model}
		cache[key] = entry
	}
	entry.users++
	return entry.model, sync.OnceFunc(func() {
		e.loads.Lock()
		if cache[key] != entry {
			e.loads.Unlock()
			return
		}
		entry.users--
		if entry.users > 0 {
			e.loads.Unlock()
			return
		}
		// Keep ORT alive after detaching the model from the cache.
		e.mu.RLock()
		delete(cache, key)
		e.loads.Unlock()
		entry.model.close()
		e.mu.RUnlock()
		releaseNativeMemory()
	}), nil
}

func closeModels[K comparable, T interface{ close() }](cache map[K]*cachedModel[T]) {
	for key, entry := range cache {
		entry.model.close()
		delete(cache, key)
	}
}

// Serialize cache changes while allowing inference. The read lock keeps Close
// from destroying the environment during session creation.
func (e *Engine) beginLoad() error {
	e.loads.Lock()
	e.mu.RLock()
	if e.ort == nil {
		e.endLoad()
		return fmt.Errorf("ORT engine is closed")
	}
	return nil
}

func (e *Engine) endLoad() {
	e.mu.RUnlock()
	e.loads.Unlock()
}

// checkAudio rejects a closed engine, a cancelled context, empty audio and
// non-finite samples. The caller must hold the engine's read lock.
func (e *Engine) checkAudio(ctx context.Context, audio []float32) error {
	if e.ort == nil {
		return fmt.Errorf("ORT engine is closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(audio) == 0 {
		return fmt.Errorf("audio phrase is empty")
	}
	for i, sample := range audio {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return fmt.Errorf("audio sample %d is not finite", i)
		}
	}
	return nil
}

func (e *Engine) session(path string, threads int) (*ort.Session, error) {
	options, err := e.ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Close()
	if err := options.SetIntraOpNumThreads(threads); err != nil {
		return nil, err
	}
	return e.ort.NewSession(path, options)
}
