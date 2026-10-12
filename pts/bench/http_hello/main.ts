// HTTP hello world, the twin of main.pts on Node's node:http (one process, one loop).
import { createServer } from "node:http";

createServer((request, response) => {
    response.writeHead(200, { "content-type": "text/plain;charset=UTF-8" });
    response.end("Hello, World!");
}).listen(Number(process.env.PORT ?? 3000));
