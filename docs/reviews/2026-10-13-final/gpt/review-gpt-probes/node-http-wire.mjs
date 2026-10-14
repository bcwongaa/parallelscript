import http from 'node:http';
const server = http.createServer(async (request, response) => {
  try {
    let body = '';
    for await (const chunk of request) body += chunk.toString('utf8');
    if (request.url === '/json') {
      response.setHeader('content-type', 'application/json');
      response.end(JSON.stringify(JSON.parse(body)));
    } else response.end('ok');
  } catch (error) {
    response.statusCode = 400;
    response.end(error.message);
  }
});
server.listen(0, '127.0.0.1', () => console.log(server.address().port));
setTimeout(() => server.closeAllConnections(), 10000).unref();
