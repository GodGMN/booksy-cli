# Builds the docs site and serves it with nginx. Build from the repository
# root, since the site publishes scripts/install.sh:
#   docker build -f docs-site/Dockerfile -t booksy-docs .
FROM node:22-alpine AS build
WORKDIR /repo/docs-site
COPY scripts/install.sh /repo/scripts/install.sh
# npm ci runs fumadocs-mdx, which reads source.config.ts and the content.
COPY docs-site/ ./
RUN npm ci
ARG SITE_URL=https://booksy.gimeno.dev
ENV SITE_URL=$SITE_URL NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM nginx:1.29-alpine
COPY docs-site/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /repo/docs-site/out /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK CMD wget -qO- http://127.0.0.1/docs/ >/dev/null || exit 1
