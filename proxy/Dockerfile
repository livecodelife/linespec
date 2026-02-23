FROM node:20-alpine

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY proxy/mocks.yaml ./mocks.yaml

RUN npm install --omit=dev

EXPOSE 3307

CMD ["node", "dist/proxy-server.js", "--mocks", "mocks.yaml", "--port", "3307"]
