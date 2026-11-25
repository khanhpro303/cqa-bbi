import { Buffer } from 'node:buffer';
import { readFileSync } from 'node:fs';
import { generateQR } from 'vietqr';

const input = JSON.parse(readFileSync(0, 'utf8'));
const { code, data } = await generateQR({
  acqId: input.bankBin,
  accountNo: input.accountNo,
  accountName: input.accountName,
  amount: input.amount,
  addInfo: input.memo,
  media: 'png',
});

const pngPrefix = 'data:image/png;base64,';
if (code !== '00' || !data?.qrDataURL?.startsWith(pngPrefix)) {
  process.exitCode = 1;
} else {
  process.stdout.write(Buffer.from(data.qrDataURL.slice(pngPrefix.length), 'base64'));
}
