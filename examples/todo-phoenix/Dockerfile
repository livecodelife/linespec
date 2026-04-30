FROM node:22-alpine
WORKDIR /app
COPY package*.json ./
RUN npm install
COPY . .
CMD ["npx", "tsx", "src/server.ts"]
