//! Heap allocations counted per thread. A world draws its frames on the test's thread, so
//! a test binary that declares `Counting` as its global allocator can hold a frame's work to a
//! budget, as a count that a loaded machine does not move.

use std::alloc::{GlobalAlloc, Layout, System};
use std::cell::Cell;

thread_local!(static MADE: Cell<u64> = const { Cell::new(0) });

/// The system allocator, counting each allocation against the thread that makes it.
pub struct Counting;

// SAFETY: every call goes to `System` unchanged; counting touches only a thread-local cell.
unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let _ = MADE.try_with(|made| made.set(made.get() + 1));
        // SAFETY: the caller's contract for `alloc` is `System::alloc`'s.
        unsafe { System.alloc(layout) }
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        // SAFETY: `ptr` came from `System` with this layout.
        unsafe { System.dealloc(ptr, layout) }
    }
}

/// The allocations this thread has made since it started.
pub fn made_here() -> u64 {
    MADE.with(Cell::get)
}
