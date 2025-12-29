# Build stage
FROM node:20-alpine AS builder

WORKDIR /app

# Build argument for API URL (Vite env vars are build-time only)
# Default empty string = use relative URLs (works with nginx proxy)
ARG VITE_API_BASE_URL=

# Set environment variable for Vite build
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL

# Copy package files
COPY package.json package-lock.json ./

# Install dependencies
RUN npm ci

# Copy source code
COPY . .

# Build the application with the API URL baked in
RUN npm run build

# Production stage
FROM nginx:alpine

# Copy built assets from builder
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy nginx configuration
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Expose port
EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
