FROM node:16-alpine

RUN mkdir -p /home/node/app/node_modules && chown -R node:node /home/node/app

WORKDIR /home/node/app

COPY package.json ./

USER node

RUN npm install --production && npm cache clean --force

COPY --chown=node:node . .

CMD [ "node", "--trace-warnings", "index.js" ]