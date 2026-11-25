package script

/*
#include <stddef.h>

typedef struct {
	void *(*calloc)(void *opaque, size_t count, size_t size);
	void *(*malloc)(void *opaque, size_t size);
	void (*free)(void *opaque, void *ptr);
	void *(*realloc)(void *opaque, void *ptr, size_t size);
	size_t (*usable)(const void *ptr);
} heap_functions;

// JSRuntime's first fields, read from quickjs-go v0.7.8 (quickjs-ng v0.18.0).
typedef struct {
	heap_functions functions;
	size_t count, size, limit;
	void *opaque;
} heap_head;

static heap_head *heap;
static heap_functions engine;
static size_t heap_limit;
static int heap_refused;

static int heap_over(size_t more) {
	if (heap->size + more < heap_limit) return 0;
	heap_refused = 1;
	return 1;
}

static void *heap_calloc(void *opaque, size_t count, size_t size) {
	return heap_over(count * size) ? NULL : engine.calloc(opaque, count, size);
}

static void *heap_malloc(void *opaque, size_t size) {
	return heap_over(size) ? NULL : engine.malloc(opaque, size);
}

static void *heap_realloc(void *opaque, void *ptr, size_t size) {
	size_t had = ptr ? engine.usable(ptr) : 0;
	return size > had && heap_over(size - had) ? NULL : engine.realloc(opaque, ptr, size);
}

static int heap_take_over(void *rt, size_t limit) {
	heap_head *head = rt;
	if (head->limit != limit || head->size == 0 || head->size > limit) return 0;
	heap = head;
	engine = head->functions;
	heap_limit = limit;
	head->functions.calloc = heap_calloc;
	head->functions.malloc = heap_malloc;
	head->functions.realloc = heap_realloc;
	head->limit = 0;
	return 1;
}

static void heap_set_limit(size_t limit) { heap_limit = limit; }
static int heap_was_refused(void) { return heap_refused; }
*/
import "C"

import (
	"errors"
	"reflect"

	"github.com/buke/quickjs-go"
)

func limitHeap(rt *quickjs.Runtime, limit int) error {
	ref := reflect.ValueOf(rt).Elem().FieldByName("ref")
	if ref.Kind() != reflect.Pointer || C.heap_take_over(ref.UnsafePointer(), C.size_t(limit)) == 0 {
		return errors.New("QuickJS's runtime is not laid out as internal/script/heap.go expects")
	}
	return nil
}

func raiseHeapLimit(limit int) { C.heap_set_limit(C.size_t(limit)) }

func heapWasRefused() bool { return C.heap_was_refused() != 0 }
