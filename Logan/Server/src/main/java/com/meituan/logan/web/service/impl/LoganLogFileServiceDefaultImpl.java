package com.meituan.logan.web.service.impl;

import com.meituan.logan.web.enums.ResultEnum;
import com.meituan.logan.web.parser.LoganProtocol;
import com.meituan.logan.web.service.LoganLogFileService;
import org.apache.commons.lang3.StringUtils;
import org.apache.log4j.Logger;
import org.springframework.stereotype.Service;

import java.io.InputStream;

/**
 * 日志文件存储服务（OSS 实现）
 * <p>
 * 接收客户端上报的加密压缩流，解密后以压缩格式上传到 OSS 存储。
 * 不写本地文件，不解压，减少存储体积 5~10 倍。
 * </p>
 */
@Service
public class LoganLogFileServiceDefaultImpl implements LoganLogFileService {

    private static final Logger LOGGER = Logger.getLogger(LoganLogFileServiceDefaultImpl.class);

    @Override
    public ResultEnum write(InputStream inputStream, String fileName) {
        if (inputStream == null || StringUtils.isEmpty(fileName)) {
            return ResultEnum.ERROR_PARAM;
        }
        try {
            return new LoganProtocol(inputStream, fileName).process();
        } catch (Exception e) {
            LOGGER.error(e);
        }
        return ResultEnum.EXCEPTION;
    }
}
