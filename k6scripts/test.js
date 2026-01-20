import http from 'k6/http';
import { check, sleep } from 'k6';
import { randomItem } from 'https://jslib.k6.io/k6-utils/1.2.0/index.js';

export const options = {
  vus: 20,           
  duration: '5m',
};

const API_URL = __ENV.API_URL || 'https://slr.itops.space/graphql/';
const CHANNEL = 'default-channel';

// Запрос товаров (публичный)
const queryProducts = `
  query Products {
    products(first: 20, channel: "${CHANNEL}") {
      edges {
        node {
          id
          variants { id }
        }
      }
    }
  }
`;

// Создание чекаута (как гость, передаем email в input)
const mutationCreateCheckout = `
  mutation CreateCheckout($variantId: ID!, $email: String!) {
    checkoutCreate(
      input: {
        channel: "${CHANNEL}",
        email: $email,
        lines: [{quantity: 1, variantId: $variantId}]
      }
    ) {
      checkout { id }
      errors { field message code }
    }
  }
`;

export default function () {
  const headers = { 'Content-Type': 'application/json' };
  
  // Генерируем случайный email для каждого теста, чтобы эмулировать разных гостей
  const email = `guest_${__VU}_${__ITER}@example.com`;

  // 1. КАТАЛОГ (Без токена)
  const resProducts = http.post(API_URL, JSON.stringify({ query: queryProducts }), { headers: headers });
  
  let variantId = null;
  try {
    const edges = resProducts.json('data.products.edges');
    if (edges && edges.length > 0) {
      const randomProduct = randomItem(edges);
      if (randomProduct.node.variants.length > 0) {
        variantId = randomProduct.node.variants[0].id;
      }
    }
  } catch (e) {}

  // 2. ПОКУПКА (Как гость)
  if (variantId) {
    const resCheckout = http.post(API_URL, JSON.stringify({
      query: mutationCreateCheckout,
      variables: { variantId: variantId, email: email }
    }), { headers: headers });

    const checkoutId = resCheckout.json('data.checkoutCreate.checkout.id');

    if (!check(resCheckout, { 'Checkout Created': (r) => checkoutId !== undefined })) {
        console.error(`Checkout Failed: ${JSON.stringify(resCheckout.json('data.checkoutCreate.errors'))}`);
    }
  }

  sleep(1);
}
