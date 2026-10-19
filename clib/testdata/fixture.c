// The fixture library for package clib's tests: plain C, built with the
// system cc at test time. The framework itself never builds or loads it.

#include <errno.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

// ---- Errors through caller-owned storage (ZapFast's zf_err, D49) ----

typedef struct {
	int32_t code;
	uint32_t len;
	uint8_t msg[256];
} fx_err;

static void set_err(fx_err *e, int32_t code, const char *fmt, ...) {
	va_list ap;
	va_start(ap, fmt);
	int n = vsnprintf((char *)e->msg, sizeof e->msg, fmt, ap);
	va_end(ap);
	if (n < 0) n = 0;
	if (n >= (int)sizeof e->msg) n = sizeof e->msg - 1;
	e->code = code;
	e->len = (uint32_t)n;
}

uint64_t fx_thread(void) { return (uint64_t)(uintptr_t)pthread_self(); }

// fx_try fails unless tag is a multiple of 3, naming the tag and the thread
// in the error. *tid gets the thread either way. The error is left
// untouched on success.
int32_t fx_try(int64_t tag, fx_err *err, uint64_t *tid) {
	*tid = fx_thread();
	if (tag % 3 == 0) return 0;
	set_err(err, (int32_t)(tag % 1000) + 1, "tag %lld failed on thread %llu", (long long)tag,
	        (unsigned long long)*tid);
	return -1;
}

// A thread-local "last error", the pattern clib cannot serve without pinning.
static __thread int32_t tls_err;
int32_t fx_tls_fail(int32_t code) { tls_err = code; return -1; }
int32_t fx_tls_last(void) { return tls_err; }

// fx_send has zf_send's shape: a handle, a buffer, its length, the error.
typedef struct { uint64_t sum, calls; } fx_core;
fx_core *fx_core_new(void) { return calloc(1, sizeof(fx_core)); }
void fx_core_free(fx_core *c) { free(c); }
uint64_t fx_core_sum(const fx_core *c) { return c->sum; }

int32_t fx_send(fx_core *c, const uint8_t *cmd, size_t len, fx_err *err) {
	if (len == 0 || cmd[0] == 0xff) {
		set_err(err, 22, "bad command (%zu bytes)", len);
		return -1;
	}
	for (size_t i = 0; i < len; i++) c->sum += cmd[i];
	c->calls++;
	return 0;
}

size_t fx_strlen(const char *s) { return strlen(s); }

// ---- Scalars ----

int64_t fx_add(int64_t a, int64_t b) { return a + b; }
uint64_t fx_mul_u64(uint64_t a, uint64_t b) { return a * b; }
int32_t fx_neg32(int32_t a) { return -a; }
uint32_t fx_not32(uint32_t a) { return ~a; }
int32_t fx_widen_i8(int8_t v) { return v; }
int32_t fx_widen_u8(uint8_t v) { return v; }
int32_t fx_widen_i16(int16_t v) { return v; }
int32_t fx_widen_u16(uint16_t v) { return v; }
int32_t fx_widen_bool(bool b) { return b ? 7 : 3; }
int8_t fx_ret_i8(int32_t v) { return (int8_t)v; }
uint8_t fx_ret_u8(int32_t v) { return (uint8_t)v; }
int16_t fx_ret_i16(int32_t v) { return (int16_t)v; }
uint16_t fx_ret_u16(int32_t v) { return (uint16_t)v; }
bool fx_ret_bool(int32_t v) { return v != 0; }
void *fx_ptr_add(void *p, size_t n) { return (char *)p + n; }
float fx_mulf(float a, float b) { return a * b; }
double fx_div(double a, double b) { return a / b; }
float fx_scalef(int32_t k, float x) { return (float)k * x; }
void fx_void(int64_t *p) { *p += 1; }

// fx_mix interleaves integer and float parameters of every width, records
// what arrived, and returns the sum as a double.
typedef struct {
	int32_t a;
	double b;
	int64_t c;
	float d;
	uint8_t e;
	double f;
	bool g;
	void *h;
	float i;
	int16_t j;
} fx_mix_rec;

double fx_mix(fx_mix_rec *o, int32_t a, double b, int64_t c, float d, uint8_t e, double f, bool g,
              void *h, float i, int16_t j) {
	o->a = a; o->b = b; o->c = c; o->d = d; o->e = e;
	o->f = f; o->g = g; o->h = h; o->i = i; o->j = j;
	return a + b + (double)c + d + e + f + g + i + j;
}

// ---- Register and stack limits ----

// fx_stack fills the integer and float registers on both ABIs, then passes
// narrow, wide and float arguments on the stack: 10 stack arguments on
// arm64, the full 12 slots on amd64.
typedef struct {
	int64_t r[7];
	double d[8];
	int8_t s0;
	int16_t s1;
	bool s2;
	int32_t s3;
	uint8_t s4;
	float s5;
	int64_t s6;
	uint16_t s7;
	double s8;
	bool s9;
} fx_stack_rec;

void fx_stack(fx_stack_rec *o, int64_t r0, int64_t r1, int64_t r2, int64_t r3, int64_t r4, int64_t r5,
              int64_t r6, double d0, double d1, double d2, double d3, double d4, double d5, double d6,
              double d7, int8_t s0, int16_t s1, bool s2, int32_t s3, uint8_t s4, float s5, int64_t s6,
              uint16_t s7, double s8, bool s9) {
	int64_t r[7] = {r0, r1, r2, r3, r4, r5, r6};
	double d[8] = {d0, d1, d2, d3, d4, d5, d6, d7};
	memcpy(o->r, r, sizeof r);
	memcpy(o->d, d, sizeof d);
	o->s0 = s0; o->s1 = s1; o->s2 = s2; o->s3 = s3; o->s4 = s4;
	o->s5 = s5; o->s6 = s6; o->s7 = s7; o->s8 = s8; o->s9 = s9;
}

// The most integer arguments a call can take: 6 registers + 12 stack slots
// on amd64, 8 + 12 on arm64. The first is where the rest are recorded.
int64_t fx_ints18(int64_t *o, int64_t a1, int64_t a2, int64_t a3, int64_t a4, int64_t a5, int64_t a6,
                  int64_t a7, int64_t a8, int64_t a9, int64_t a10, int64_t a11, int64_t a12, int64_t a13,
                  int64_t a14, int64_t a15, int64_t a16, int64_t a17) {
	int64_t a[] = {a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17};
	int64_t s = 0;
	for (int i = 0; i < 17; i++) { o[i] = a[i]; s += a[i]; }
	return s;
}

int64_t fx_ints20(int64_t *o, int64_t a1, int64_t a2, int64_t a3, int64_t a4, int64_t a5, int64_t a6,
                  int64_t a7, int64_t a8, int64_t a9, int64_t a10, int64_t a11, int64_t a12, int64_t a13,
                  int64_t a14, int64_t a15, int64_t a16, int64_t a17, int64_t a18, int64_t a19) {
	int64_t a[] = {a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17, a18, a19};
	int64_t s = 0;
	for (int i = 0; i < 19; i++) { o[i] = a[i]; s += a[i]; }
	return s;
}

// The most float arguments: 8 registers + 12 stack slots on both ABIs.
double fx_floats20(double *o, double a0, double a1, double a2, double a3, double a4, double a5,
                   double a6, double a7, double a8, double a9, double a10, double a11, double a12,
                   double a13, double a14, double a15, double a16, double a17, double a18, double a19) {
	double a[] = {a0, a1, a2, a3, a4, a5, a6, a7, a8, a9, a10, a11, a12, a13, a14, a15, a16, a17, a18, a19};
	double s = 0;
	for (int i = 0; i < 20; i++) { o[i] = a[i]; s += a[i]; }
	return s;
}

// ---- Blocking, spinning and signals ----

static int64_t now_ns(void) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// fx_sleep sleeps for ns, resuming after each signal, and returns how many
// times a signal interrupted it. *tid holds the calling thread while it
// sleeps and 0 after; *started is set to 1 once it has.
int32_t fx_sleep(int64_t ns, uint64_t *tid, int32_t *started) {
	if (tid) __atomic_store_n(tid, fx_thread(), __ATOMIC_SEQ_CST);
	if (started) __atomic_store_n(started, 1, __ATOMIC_SEQ_CST);
	int32_t eintr = 0;
	int64_t end = now_ns() + ns;
	for (;;) {
		int64_t left = end - now_ns();
		if (left <= 0) break;
		struct timespec ts = {left / 1000000000, left % 1000000000};
		if (nanosleep(&ts, NULL) != 0 && errno == EINTR) eintr++;
	}
	if (tid) __atomic_store_n(tid, 0, __ATOMIC_SEQ_CST);
	return eintr;
}

// fx_spin runs n steps of xorshift64 from seed, with a double that must
// stay exactly 1.0 across signal handlers; *fp gets it. *tid is as for
// fx_sleep.
uint64_t fx_spin(uint64_t n, uint64_t seed, double *fp, uint64_t *tid) {
	if (tid) __atomic_store_n(tid, fx_thread(), __ATOMIC_SEQ_CST);
	uint64_t x = seed;
	double d = 1.0;
	for (uint64_t i = 0; i < n; i++) {
		x ^= x << 13;
		x ^= x >> 7;
		x ^= x << 17;
		d = d * 1.5;
		d = d / 1.5;
	}
	*fp = d;
	if (tid) __atomic_store_n(tid, 0, __ATOMIC_SEQ_CST);
	return x;
}

int32_t fx_kill(uint64_t tid, int32_t sig) { return pthread_kill((pthread_t)(uintptr_t)tid, sig); }

// Foreign threads: the library's own, as a Rust async runtime would have.
// Half spin, half block in short sleeps, until fx_threads_stop.
#define FX_MAX_THREADS 16
static pthread_t fx_tids[FX_MAX_THREADS];
static int fx_nthreads;
static int fx_stop_flag;
static uint64_t fx_iters;

static void *spinner(void *arg) {
	(void)arg;
	while (!__atomic_load_n(&fx_stop_flag, __ATOMIC_RELAXED)) {
		volatile double x = 0;
		for (int i = 0; i < 100000; i++) x += i * 0.5;
		__atomic_add_fetch(&fx_iters, 1, __ATOMIC_RELAXED);
	}
	return NULL;
}

static void *sleeper(void *arg) {
	(void)arg;
	while (!__atomic_load_n(&fx_stop_flag, __ATOMIC_RELAXED)) {
		struct timespec ts = {0, 1000000};
		nanosleep(&ts, NULL);
		__atomic_add_fetch(&fx_iters, 1, __ATOMIC_RELAXED);
	}
	return NULL;
}

int32_t fx_threads_start(int32_t n) {
	if (n > FX_MAX_THREADS) n = FX_MAX_THREADS;
	__atomic_store_n(&fx_stop_flag, 0, __ATOMIC_SEQ_CST);
	__atomic_store_n(&fx_iters, 0, __ATOMIC_SEQ_CST);
	for (fx_nthreads = 0; fx_nthreads < n; fx_nthreads++) {
		if (pthread_create(&fx_tids[fx_nthreads], NULL, fx_nthreads % 2 ? sleeper : spinner, NULL) != 0)
			return -1;
	}
	return 0;
}

uint64_t fx_threads_handle(int32_t i) { return (uint64_t)(uintptr_t)fx_tids[i]; }

uint64_t fx_threads_stop(void) {
	__atomic_store_n(&fx_stop_flag, 1, __ATOMIC_SEQ_CST);
	for (int i = 0; i < fx_nthreads; i++) pthread_join(fx_tids[i], NULL);
	fx_nthreads = 0;
	return __atomic_load_n(&fx_iters, __ATOMIC_SEQ_CST);
}
