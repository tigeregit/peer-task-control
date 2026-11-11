// Tiny CUDA vector add used to verify a real NVIDIA GPU inside an official
// nvidia/cuda Ubuntu container. Keep this file small; do not vendor the toolkit.
#include <cstdio>
#include <cuda_runtime.h>

__global__ void add(const int *a, const int *b, int *c, int n) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < n) c[i] = a[i] + b[i];
}

int main() {
    const int n = 1 << 16;
    int *ha = new int[n], *hb = new int[n], *hc = new int[n];
    for (int i = 0; i < n; i++) { ha[i] = i; hb[i] = 2 * i; }
    int *da, *db, *dc;
    cudaMalloc(&da, n * sizeof(int));
    cudaMalloc(&db, n * sizeof(int));
    cudaMalloc(&dc, n * sizeof(int));
    cudaMemcpy(da, ha, n * sizeof(int), cudaMemcpyHostToDevice);
    cudaMemcpy(db, hb, n * sizeof(int), cudaMemcpyHostToDevice);
    add<<<(n + 255) / 256, 256>>>(da, db, dc, n);
    cudaMemcpy(hc, dc, n * sizeof(int), cudaMemcpyDeviceToHost);
    cudaError_t err = cudaGetLastError();
    if (err != cudaSuccess) {
        std::fprintf(stderr, "CUDA error: %s\n", cudaGetErrorString(err));
        return 1;
    }
    for (int i = 0; i < n; i++) {
        if (hc[i] != ha[i] + hb[i]) {
            std::fprintf(stderr, "mismatch at %d: %d\n", i, hc[i]);
            return 1;
        }
    }
    int dev = 0;
    cudaDeviceProp prop{};
    cudaGetDeviceProperties(&prop, dev);
    std::printf("ok vector_add n=%d device=%s cc=%d.%d\n", n, prop.name, prop.major, prop.minor);
    delete[] ha; delete[] hb; delete[] hc;
    cudaFree(da); cudaFree(db); cudaFree(dc);
    return 0;
}
