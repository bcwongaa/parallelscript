// HTTP hello world, the twin of main.pts on Bun.serve (one process, one loop).
Bun.serve({
    port: Number(process.env.PORT ?? 3000),
    fetch: () => new Response("Hello, World!"),
});
