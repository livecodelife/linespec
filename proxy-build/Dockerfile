FROM node:20-alpine

WORKDIR /app

COPY package.json ./
COPY dist ./dist
COPY mocks.yaml ./

RUN npm install --omit=dev

EXPOSE 3306 3308 9092

CMD ["node", "dist/proxy-server.js", "--mocks", "mocks.yaml", "--port", "3306", "--control-port", "3308"]
