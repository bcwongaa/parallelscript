// HTTP JSON, the twin of main.pts on Bun.serve (one process, one loop).
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

Bun.serve({
    port: Number(process.env.PORT ?? 3000),
    async fetch(request: Request) {
        const order: Order = await request.json();
        let total = 0;
        for (const item of order.items) {
            total += item.quantity * item.price;
        }
        return Response.json({ id: order.id, customer: order.customer, items: order.items.length, total });
    },
});
