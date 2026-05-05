import axios from "axios";

const ORDERS_API = "http://localhost:8082/api/orders";

export async function placeOrder(product) {
  const response = await axios.post(ORDERS_API, product);
  return response.data;
}
