package com.meituan.logan.web.parser;

import com.meituan.logan.web.enums.ResultEnum;
import com.meituan.logan.web.util.OssUtil;
import org.apache.commons.io.IOUtils;
import org.apache.commons.lang3.ArrayUtils;
import org.apache.log4j.Logger;
import org.bouncycastle.jce.provider.BouncyCastleProvider;
import org.springframework.core.io.support.PropertiesLoaderUtils;

import javax.crypto.BadPaddingException;
import javax.crypto.Cipher;
import javax.crypto.spec.IvParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import java.io.*;
import java.nio.ByteBuffer;
import java.security.Security;
import java.util.Properties;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.zip.GZIPInputStream;

/**
 * Logan 日志协议处理器
 * <p>
 * 改造后：解密后存储压缩数据到 OSS（不解压），减少存储体积 5~10 倍。
 * 下载时由 OSS 签名 URL 直连，后端不参与文件传输。
 * </p>
 *
 * @since logan-web 1.0
 */
public class LoganProtocol {

    private static final Logger LOGGER = Logger.getLogger(LoganProtocol.class);

    private static final char ENCRYPT_CONTENT_START = '\1';

    private static final String AES_NO_PADDING = "AES/CBC/NoPadding";
    private static final String AES_WITH_PADDING = "AES/CBC/PKCS5Padding";

    private static AtomicBoolean initialized = new AtomicBoolean(false);

    static {
        initialize();
    }

    private byte[] secretKey;
    private byte[] iv;
    private ByteBuffer wrap;
    private String fileName;

    /**
     * 构造函数
     *
     * @param stream   上传的加密压缩流
     * @param fileName 日志文件名（用于 OSS key）
     */
    public LoganProtocol(InputStream stream, String fileName) {
        try {
            wrap = ByteBuffer.wrap(IOUtils.toByteArray(stream));
            this.fileName = fileName;
        } catch (IOException e) {
            LOGGER.error(e);
        }
    }

    /**
     * 处理日志：解密 → 写压缩数据到临时文件 → 上传 OSS → 删除临时文件
     *
     * @return SUCCESS 或 ERROR_DECRYPT
     */
    public ResultEnum process() {
        if (!initSecureParams()) {
            return ResultEnum.ERROR_DECRYPT;
        }

        File tempFile = null;
        FileOutputStream tempOutputStream = null;
        try {
            // 创建临时文件，存储解密后的压缩数据
            tempFile = File.createTempFile("logan_", ".gz");
            tempOutputStream = new FileOutputStream(tempFile);

            while (wrap.hasRemaining()) {
                while (wrap.hasRemaining() && wrap.get() == ENCRYPT_CONTENT_START) {
                    byte[] encrypt = new byte[wrap.getInt()];
                    if (!tryGetEncryptContent(encrypt) || !decryptAndAppend(tempOutputStream, encrypt)) {
                        return ResultEnum.ERROR_DECRYPT;
                    }
                }
            }
            tempOutputStream.flush();
            tempOutputStream.close();

            // 上传压缩数据到 OSS
            boolean uploaded = OssUtil.uploadFile(fileName, tempFile);
            if (!uploaded) {
                LOGGER.error("Failed to upload compressed log to OSS: " + fileName);
                return ResultEnum.ERROR_DECRYPT;
            }

            return ResultEnum.SUCCESS;

        } catch (Exception e) {
            LOGGER.error("LoganProtocol.process failed", e);
            return ResultEnum.ERROR_DECRYPT;
        } finally {
            closeQuietly(tempOutputStream);
            // 删除临时文件
            if (tempFile != null && tempFile.exists()) {
                tempFile.delete();
            }
        }
    }

    private boolean tryGetEncryptContent(byte[] encrypt) {
        try {
            wrap.get(encrypt);
        } catch (java.nio.BufferUnderflowException e) {
            LOGGER.error(e);
            return false;
        }
        return true;
    }

    /**
     * 解密但不解压，将压缩数据写入输出流（用于存储到 OSS）
     */
    private boolean decryptAndAppend(OutputStream outputStream, byte[] encrypt) {
        boolean result = false;
        try {
            SecretKeySpec secretKeySpec = new SecretKeySpec(secretKey, "AES");
            IvParameterSpec ivParamSpec = new IvParameterSpec(iv);

            Cipher cipher;
            try {
                // 先尝试带 padding 模式解密末尾 16 字节
                cipher = Cipher.getInstance(AES_WITH_PADDING);
                cipher.init(Cipher.DECRYPT_MODE, secretKeySpec, ivParamSpec);
                cipher.doFinal(encrypt, encrypt.length - 16, 16);
            } catch (BadPaddingException e) {
                LOGGER.warn("decrypt with padding mode fail", e);
                // 带 padding 模式解密失败，尝试无 padding 模式
                cipher = Cipher.getInstance(AES_NO_PADDING);
            }

            cipher.init(Cipher.DECRYPT_MODE, secretKeySpec, ivParamSpec);
            byte[] compressed = cipher.doFinal(encrypt);
            // 不解压，直接写入压缩数据
            outputStream.write(compressed);
            outputStream.flush();
            result = true;
        } catch (Exception e) {
            LOGGER.error("decryptAndAppend", e);
        }
        return result;
    }

    /**
     * 解压 GZIP 数据为明文（供 tryAnalyze 使用）
     *
     * @param compressedBytes GZIP 压缩的数据
     * @return 解压后的明文
     */
    public static byte[] decompress(byte[] compressedBytes) {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        try (GZIPInputStream gzipInputStream = new GZIPInputStream(new ByteArrayInputStream(compressedBytes))) {
            IOUtils.copy(gzipInputStream, out);
            return out.toByteArray();
        } catch (IOException e) {
            // 虽然解压抛了异常，但前面已经解出来的内容还是可用的
            // 由于多条日志使用 \n 分割，这里取最后一个 \n 前的内容
            byte[] arr = out.toByteArray();
            int lastIndexOfLf = ArrayUtils.lastIndexOf(arr, (byte) '\n');
            arr = lastIndexOfLf < 0 ? new byte[0] : ArrayUtils.subarray(arr, 0, lastIndexOfLf + 1);
            LOGGER.error("decompress, dropped=" + (out.size() - arr.length), e);
            return arr;
        }
    }

    /**
     * BouncyCastle作为安全提供，防止我们加密解密时候因为jdk内置的不支持改模式运行报错。
     **/
    private static void initialize() {
        if (initialized.get()) {
            return;
        }
        Security.addProvider(new BouncyCastleProvider());
        initialized.set(true);
    }

    private boolean initSecureParams() {
        if (checkSecureParams()) {
            return true;
        }
        try {
            Properties properties = PropertiesLoaderUtils.loadAllProperties("secure.properties");
            secretKey = properties.getProperty("AES_KEY").getBytes();
            iv = properties.getProperty("IV").getBytes();
            return checkSecureParams();
        } catch (IOException e) {
            LOGGER.error("initSecureParams", e);
        }
        return false;
    }

    private boolean checkSecureParams() {
        return secretKey != null && secretKey.length == 16 &&
                iv != null && iv.length == 16;
    }

    private static void closeQuietly(Closeable closeable) {
        if (closeable != null) {
            try {
                closeable.close();
            } catch (IOException ignored) {
            }
        }
    }
}
