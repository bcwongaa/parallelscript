// The Mandelbrot set: the escape time of every pixel of a 2400×1600 view (at
// most 500 iterations) into an image, row by row; prints the escape times' total
// and the pixels inside the set. The twin of main.pts without `.par`, for Node and Bun.

const WIDTH = 2400;
const HEIGHT = 1600;
const MAX_ITERATIONS = 500;

function escapeTime(cx: number, cy: number): number {
    let x = 0;
    let y = 0;
    let i = 0;
    while (i < MAX_ITERATIONS && x * x + y * y <= 4) {
        const t = x * x - y * y + cx;
        y = 2 * x * y + cy;
        x = t;
        i++;
    }
    return i;
}

const image = new Uint16Array(WIDTH * HEIGHT);

function renderRow(py: number): void {
    const cy = (py / HEIGHT) * 2.4 - 1.2;
    for (let px = 0; px < WIDTH; px++) {
        image[py * WIDTH + px] = escapeTime((px / WIDTH) * 3.5 - 2.5, cy);
    }
}

for (let py = 0; py < HEIGHT; py++) {
    renderRow(py);
}
console.log(image.reduce((sum, n) => sum + n, 0), image.reduce((inside, n) => inside + (n === MAX_ITERATIONS ? 1 : 0), 0));
