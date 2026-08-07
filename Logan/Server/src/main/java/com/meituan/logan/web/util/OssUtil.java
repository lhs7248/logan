package com.meituan.logan.web.util;

import com.aliyun.oss.OSS;
import com.aliyun.oss.OSSClientBuilder;
import com.aliyun.oss.model.OSSObject;
import com.aliyun.oss.model.ObjectMetadata;
import org.apache.log4j.Logger;
import org.springframework.core.io.support.PropertiesLoaderUtils;

import java.io.File;
import java.io.InputStream;
import java.net.URL;
import java.util.Date;
import java.util.Properties;
import java.util.concurrent.TimeUnit;

/**
 * 阿里云 OSS 工具类
 *
 * @since 2026-08-06
 */
public class OssUtil {

    private static final Logger LOGGER = Logger.getLogger(OssUtil.class);

    private static final Properties properties = loadProperties();

    private static final String ENDPOINT = properties.getProperty("oss.endpoint");
    private static final String ACCESS_KEY_ID = properties.getProperty("oss.accessKeyId");
    private static final String ACCESS_KEY_SECRET = properties.getProperty("oss.accessKeySecret");
    private static final String BUCKET_NAME = properties.getProperty("oss.bucketName");
    private static final String PREFIX = properties.getProperty("oss.prefix", "logan-logs/");
    private static final int EXPIRE_DAYS = Integer.parseInt(properties.getProperty("oss.expireDays", "7"));

    private OssUtil() {
    }

    private static Properties loadProperties() {
        try {
            return PropertiesLoaderUtils.loadAllProperties("oss.properties");
        } catch (Exception e) {
            LOGGER.error("Failed to load oss.properties", e);
            return new Properties();
        }
    }

    /**
     * 上传文件到 OSS
     *
     * @param fileName 文件名（不含路径前缀）
     * @param inputStream 文件输入流
     * @return true 上传成功
     */
    public static boolean uploadFile(String fileName, InputStream inputStream) {
        String key = PREFIX + fileName;
        OSS ossClient = null;
        try {
            ossClient = new OSSClientBuilder().build(ENDPOINT, ACCESS_KEY_ID, ACCESS_KEY_SECRET);

            ObjectMetadata metadata = new ObjectMetadata();
            metadata.setContentType("application/octet-stream");

            ossClient.putObject(BUCKET_NAME, key, inputStream, metadata);
            return true;
        } catch (Exception e) {
            LOGGER.error("OSS upload failed: " + key, e);
            return false;
        } finally {
            if (ossClient != null) {
                ossClient.shutdown();
            }
        }
    }

    /**
     * 上传本地文件到 OSS
     *
     * @param fileName 文件名
     * @param file 本地文件
     * @return true 上传成功
     */
    public static boolean uploadFile(String fileName, File file) {
        String key = PREFIX + fileName;
        OSS ossClient = null;
        try {
            ossClient = new OSSClientBuilder().build(ENDPOINT, ACCESS_KEY_ID, ACCESS_KEY_SECRET);

            ObjectMetadata metadata = new ObjectMetadata();
            metadata.setContentType("application/octet-stream");

            ossClient.putObject(BUCKET_NAME, key, file, metadata);
            return true;
        } catch (Exception e) {
            LOGGER.error("OSS upload failed: " + key, e);
            return false;
        } finally {
            if (ossClient != null) {
                ossClient.shutdown();
            }
        }
    }

    /**
     * 生成签名下载 URL（有效期 1 小时）
     *
     * @param fileName 文件名
     * @return 签名 URL，失败返回 null
     */
    public static String generatePresignedUrl(String fileName) {
        String key = PREFIX + fileName;
        OSS ossClient = null;
        try {
            ossClient = new OSSClientBuilder().build(ENDPOINT, ACCESS_KEY_ID, ACCESS_KEY_SECRET);
            Date expiration = new Date(System.currentTimeMillis() + TimeUnit.HOURS.toMillis(1));
            URL url = ossClient.generatePresignedUrl(BUCKET_NAME, key, expiration);
            return url.toString();
        } catch (Exception e) {
            LOGGER.error("OSS generatePresignedUrl failed: " + key, e);
            return null;
        } finally {
            if (ossClient != null) {
                ossClient.shutdown();
            }
        }
    }

    /**
     * 从 OSS 下载文件为 InputStream（调用方负责关闭流）
     *
     * @param fileName 文件名
     * @return InputStream，失败返回 null
     */
    public static InputStream downloadFile(String fileName) {
        String key = PREFIX + fileName;
        try {
            OSS ossClient = new OSSClientBuilder().build(ENDPOINT, ACCESS_KEY_ID, ACCESS_KEY_SECRET);
            OSSObject object = ossClient.getObject(BUCKET_NAME, key);
            // 注意：不能在这里 shutdown ossClient，否则 inputStream 会关闭
            // OSSClient 会在 inputStream 关闭时自动释放
            return object.getObjectContent();
        } catch (Exception e) {
            LOGGER.error("OSS download failed: " + key, e);
            return null;
        }
    }

    /**
     * 删除 OSS 文件
     *
     * @param fileName 文件名
     * @return true 删除成功
     */
    public static boolean deleteFile(String fileName) {
        String key = PREFIX + fileName;
        OSS ossClient = null;
        try {
            ossClient = new OSSClientBuilder().build(ENDPOINT, ACCESS_KEY_ID, ACCESS_KEY_SECRET);
            ossClient.deleteObject(BUCKET_NAME, key);
            return true;
        } catch (Exception e) {
            LOGGER.error("OSS delete failed: " + key, e);
            return false;
        } finally {
            if (ossClient != null) {
                ossClient.shutdown();
            }
        }
    }

    /**
     * 获取 OSS 保留天数
     */
    public static int getExpireDays() {
        return EXPIRE_DAYS;
    }
}
