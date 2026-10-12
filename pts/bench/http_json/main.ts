// HTTP JSON, the twin of main.pts on Node's node:http (one process, one loop).
import { createServer } from "node:http";

interface Item {
    sku: string;
    quantity: number;
    price: number;
}

interface Order {
    id: number;
    customer: string;
    items: Item[];
}

createServer((request, response) => {
    let body = "";
    request.setEncoding("utf8");
    request.on("data", (chunk: string) => {
        body += chunk;
    });
    request.on("end", () => {
        const order: Order = JSON.parse(body);
        let total = 0;
        for (const item of order.items) {
            total += item.quantity * item.price;
        }
        response.writeHead(200, { "content-type": "application/json" });
        response.end(JSON.stringify({ id: order.id, customer: order.customer, items: order.items.length, total }));
    });
}).listen(Number(process.env.PORT ?? 3000));
